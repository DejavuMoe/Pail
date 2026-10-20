package rules

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type Direction string

const (
	Upload   Direction = "upload"
	Download Direction = "download"
)

type CompareMode string

const (
	SizeTime CompareMode = "size_time"
	Checksum CompareMode = "checksum"
)

type ActionKind string

const (
	ActionUpload   ActionKind = "upload"
	ActionDownload ActionKind = "download"
	ActionDelete   ActionKind = "delete"
	ActionConflict ActionKind = "conflict"
)

type Entry struct {
	Path              string    `json:"path"`
	Size              int64     `json:"size"`
	Modified          time.Time `json:"modified"`
	Checksum          string    `json:"checksum,omitempty"`
	ChecksumAlgorithm string    `json:"checksumAlgorithm,omitempty"`
}
type SyncOptions struct {
	Direction        Direction     `json:"direction"`
	Compare          CompareMode   `json:"compare"`
	DeleteExtraneous bool          `json:"deleteExtraneous"`
	OverwriteNewer   bool          `json:"overwriteNewer"`
	TimeTolerance    time.Duration `json:"timeTolerance"`
	Exclude          []string      `json:"exclude,omitempty"`
}
type SyncAction struct {
	Kind        ActionKind `json:"kind"`
	Path        string     `json:"path"`
	Target      string     `json:"target"`
	Reason      string     `json:"reason"`
	Source      *Entry     `json:"source,omitempty"`
	Destination *Entry     `json:"destination,omitempty"`
}
type SyncPlan struct {
	Actions   []SyncAction `json:"actions"`
	Unchanged int          `json:"unchanged"`
	Excluded  int          `json:"excluded"`
	Retained  int          `json:"retained"`
}

// PlanSync is a one-way, pure manifest comparison. Empty options mean upload,
// size+mtime, no deletion, and no overwrite of newer destinations. Inputs must
// be complete snapshots; partial listing MUST NOT be used with deletion enabled.
// It does not execute actions, discover files, or claim conflict-free two-way sync.
func PlanSync(local, remote []Entry, opts SyncOptions) (SyncPlan, error) {
	if opts.Direction == "" {
		opts.Direction = Upload
	}
	if opts.Compare == "" {
		opts.Compare = SizeTime
	}
	if opts.Direction != Upload && opts.Direction != Download {
		return SyncPlan{}, fmt.Errorf("invalid direction %q", opts.Direction)
	}
	if opts.Compare != SizeTime && opts.Compare != Checksum {
		return SyncPlan{}, fmt.Errorf("invalid compare mode %q", opts.Compare)
	}
	if opts.TimeTolerance < 0 {
		return SyncPlan{}, fmt.Errorf("negative time tolerance")
	}
	lm, err := indexManifest(local)
	if err != nil {
		return SyncPlan{}, fmt.Errorf("local: %w", err)
	}
	rm, err := indexManifest(remote)
	if err != nil {
		return SyncPlan{}, fmt.Errorf("remote: %w", err)
	}
	src, dst := lm, rm
	target := "remote"
	kind := ActionUpload
	if opts.Direction == Download {
		src, dst = rm, lm
		target = "local"
		kind = ActionDownload
	}
	keys := map[string]bool{}
	for k := range src {
		keys[k] = true
	}
	for k := range dst {
		keys[k] = true
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	plan := SyncPlan{Actions: []SyncAction{}}
	for _, key := range ordered {
		excluded := false
		for _, mask := range opts.Exclude {
			if Match(mask, key) {
				excluded = true
				break
			}
		}
		if excluded {
			plan.Excluded++
			continue
		}
		s, hasSource := src[key]
		d, hasDest := dst[key]
		action := SyncAction{Path: key, Target: target}
		if hasSource {
			v := s
			action.Source = &v
		}
		if hasDest {
			v := d
			action.Destination = &v
		}
		if !hasSource {
			if opts.DeleteExtraneous {
				action.Kind = ActionDelete
				action.Reason = "destination-only"
				plan.Actions = append(plan.Actions, action)
			} else {
				plan.Retained++
			}
			continue
		}
		if !hasDest {
			action.Kind = kind
			action.Reason = "source-only"
			plan.Actions = append(plan.Actions, action)
			continue
		}
		same, reason := compareEntries(s, d, opts)
		if reason != "" {
			action.Kind = ActionConflict
			action.Reason = reason
			plan.Actions = append(plan.Actions, action)
			continue
		}
		if same {
			plan.Unchanged++
			continue
		}
		if !opts.OverwriteNewer && (s.Modified.IsZero() || d.Modified.IsZero()) {
			action.Kind = ActionConflict
			action.Reason = "missing modification time for overwrite decision"
		} else if !opts.OverwriteNewer && d.Modified.After(s.Modified) && d.Modified.Sub(s.Modified) > opts.TimeTolerance {
			action.Kind = ActionConflict
			action.Reason = "destination is newer"
		} else {
			action.Kind = kind
			action.Reason = "content differs"
		}
		plan.Actions = append(plan.Actions, action)
	}
	return plan, nil
}
func compareEntries(s, d Entry, o SyncOptions) (bool, string) {
	if o.Compare == Checksum {
		if s.Checksum == "" || d.Checksum == "" || s.ChecksumAlgorithm == "" || d.ChecksumAlgorithm == "" {
			return false, "missing checksum or algorithm"
		}
		if s.ChecksumAlgorithm != d.ChecksumAlgorithm {
			return false, "checksum algorithms differ"
		}
		if s.ChecksumAlgorithm == "etag" {
			return false, "ETag is not a content checksum"
		}
		return s.Size == d.Size && s.Checksum == d.Checksum, ""
	}
	if s.Modified.IsZero() || d.Modified.IsZero() {
		return false, "missing modification time"
	}
	delta := s.Modified.Sub(d.Modified)
	if delta < 0 {
		delta = d.Modified.Sub(s.Modified)
	}
	return s.Size == d.Size && delta <= o.TimeTolerance, ""
}
func indexManifest(entries []Entry) (map[string]Entry, error) {
	out := make(map[string]Entry, len(entries))
	for _, e := range entries {
		if e.Path == "" || strings.ContainsAny(e.Path, "\\\x00\r\n") || strings.HasPrefix(e.Path, "/") || strings.Contains(e.Path, ":") {
			return nil, fmt.Errorf("unsafe manifest path %q", e.Path)
		}
		for _, part := range strings.Split(e.Path, "/") {
			if part == "" || part == "." || part == ".." {
				return nil, fmt.Errorf("unsafe manifest path %q", e.Path)
			}
		}
		if e.Size < 0 {
			return nil, fmt.Errorf("negative size for %q", e.Path)
		}
		if _, exists := out[e.Path]; exists {
			return nil, fmt.Errorf("duplicate path %q", e.Path)
		}
		e.ChecksumAlgorithm = strings.ToLower(strings.TrimSpace(e.ChecksumAlgorithm))
		out[e.Path] = e
	}
	return out, nil
}
