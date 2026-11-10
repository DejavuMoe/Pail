package icons

import (
	"strings"
	"testing"
)

func TestAllIconsParse(t *testing.T) {
	entries, _ := files.ReadDir(".")
	var names []string
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".svg"))
	}
	if len(names) < 40 {
		t.Fatalf("only %d icons embedded", len(names))
	}
	for _, n := range names {
		if Get(n) == nil {
			t.Errorf("icon %s did not parse", n)
		}
	}
	if ForKey("a/b.PNG", false) != Get("file-image") || ForKey("x/", true) != Get("folder") || ForKey("noext", false) != Get("file") {
		t.Error("extension mapping is wrong")
	}
}
