package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The programs a release ships must not link the demo environment, the
// in-memory S3 server or its httptest dependency.
func TestReleaseBuildsExcludeDemoAndTestServers(t *testing.T) {
	root, err := findRoot()
	if err != nil {
		t.Skipf("not run from the repository: %v", err)
	}
	cmd := exec.Command("go", "list", "-deps", "./cmd/pail", "./cmd/pail-desktop")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	for _, pkg := range strings.Fields(string(out)) {
		switch {
		case strings.HasSuffix(pkg, "/internal/demo"), strings.HasSuffix(pkg, "/internal/s3test"), pkg == "net/http/httptest":
			t.Errorf("a release build links %s", pkg)
		}
	}
}
