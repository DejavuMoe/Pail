package app

import (
	"fmt"
	"strings"
)

// AccountBucket is a bucket the user named for an account (Account.Buckets).
// Prefix is the folder the bucket opens in: "" for the bucket root, else a key
// prefix ending in "/".
type AccountBucket struct {
	Name   string `json:"name"`
	Prefix string `json:"prefix,omitempty"`
}

// String is the text form ParseAccountBuckets reads: "name" or "name/folder/".
func (b AccountBucket) String() string {
	if b.Prefix == "" {
		return b.Name
	}
	return b.Name + "/" + b.Prefix
}

// ParseAccountBuckets reads named buckets in their text form, one per element:
// "name" or "name/folder/" (a missing trailing "/" is added). Blank elements
// are skipped. The result is normalised as NormalizeAccountBuckets does.
func ParseAccountBuckets(list []string) ([]AccountBucket, error) {
	var out []AccountBucket
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		name, prefix, _ := strings.Cut(s, "/")
		out = append(out, AccountBucket{Name: name, Prefix: prefix})
	}
	return NormalizeAccountBuckets(out)
}

// NormalizeAccountBuckets trims the entries, completes a folder's trailing "/",
// checks the bucket names and the folders, and rejects a bucket named twice:
// the bucket list, the aliases and the sidebar are keyed by bucket name, so
// one bucket can open in only one folder. An empty result is nil (the service
// lists the buckets).
func NormalizeAccountBuckets(list []AccountBucket) ([]AccountBucket, error) {
	var out []AccountBucket
	seen := map[string]bool{}
	for _, b := range list {
		b.Name = strings.TrimSpace(b.Name)
		b.Prefix = strings.TrimSpace(b.Prefix)
		if b.Name == "" && b.Prefix == "" {
			continue
		}
		if err := bmValidateBucketName(b.Name); err != nil {
			return nil, fmt.Errorf("buckets: %s: %v", b.Name, err)
		}
		if seen[b.Name] {
			return nil, fmt.Errorf("buckets: %s is named twice; name each bucket once, with the one folder it opens in", b.Name)
		}
		seen[b.Name] = true
		if p := strings.TrimLeft(b.Prefix, "/"); p != "" {
			if !strings.HasSuffix(p, "/") {
				p += "/"
			}
			for _, seg := range strings.Split(strings.TrimSuffix(p, "/"), "/") {
				if seg == "" || seg == "." || seg == ".." {
					return nil, fmt.Errorf("buckets: %s: the folder %q must not contain empty, \".\" or \"..\" parts", b.Name, p)
				}
			}
			b.Prefix = p
		} else {
			b.Prefix = ""
		}
		out = append(out, b)
	}
	return out, nil
}
