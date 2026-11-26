package main

// viewCache holds what the frame derives from the listing, the choice, the
// folder sizes and the clipboard, each with the counters it was computed at
// (rawGen, rowsGen, sel.gen, sizeGen): a frame in which none of them moved
// reads it back instead of walking the rows again.
type viewCache struct {
	// builds counts how often each derived value was made (tests pin that a
	// frame in which nothing moved makes none).
	builds struct{ keys, selSum, folderSum, shown, sorted, clip int }

	// selectedKeys
	keys              []string
	keysSel, keysRows uint64
	keysN             int
	keysOK            bool

	// folderSummary
	sumRaw               uint64
	sumN                 int
	sumFolders, sumFiles int
	sumBytes             int64
	sumOK                bool

	// shownFolders and canSizeShown
	shown     []string
	shownRows uint64
	shownN    int
	shownOK   bool
	idle      bool
	idleRows  uint64
	idleSize  uint64
	idleN     int
	idleScope Scope
	idleOK    bool

	srt sortedCache // rebuildRows

	// the clipboard's keys as a set (isCutRow), for the slice it was made from
	clipFor []string
	clipSet map[string]struct{}
}

// inClip reports whether key is on the in-app clipboard. The set is made once
// per clipboard: a Ctrl+X of a thousand rows asked about every visible row on
// every frame would otherwise scan the thousand each time.
func (a *app) inClip(key string) bool {
	keys := a.mg.clip.keys
	c := &a.vc
	if len(keys) == 0 {
		return false
	}
	if len(keys) != len(c.clipFor) || &keys[0] != &c.clipFor[0] {
		c.builds.clip++
		set := make(map[string]struct{}, len(keys))
		for _, k := range keys {
			set[k] = struct{}{}
		}
		c.clipFor, c.clipSet = keys, set
	}
	_, ok := c.clipSet[key]
	return ok
}
