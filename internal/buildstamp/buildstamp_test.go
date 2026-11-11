package buildstamp

import "testing"

func TestParse(t *testing.T) {
	for in, want := range map[string]string{
		"pail-release-version:1.2.3;":        "1.2.3",
		"pail-release-version:0.7.0-rc.1;":   "0.7.0-rc.1",
		"pail-release-version:1.2.3":         "",
		"pail-release-version:;":             "",
		"pail-release-versio:1.2.3;":         "",
		"pail-release-version:1.2.3 x;":      "",
		"pail-release-version:1.2.3;trailer": "",
		"":                                   "",
	} {
		got, ok := Parse(in)
		if got != want || ok != (want != "") {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if Version() != "dev" {
		t.Errorf("unstamped test binary reports %q", Version())
	}
	if LDFlag("1.2.3") != "-X github.com/DejavuMoe/Pail/internal/buildstamp.stamp=pail-release-version:1.2.3;" {
		t.Error(LDFlag("1.2.3"))
	}
}
