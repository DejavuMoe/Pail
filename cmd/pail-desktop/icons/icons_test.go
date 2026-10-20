package icons

import "testing"

func TestAllIconsParse(t *testing.T) {
	names := Names()
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
