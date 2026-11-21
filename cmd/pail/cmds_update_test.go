package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/selfupdate"
)

// fakeUpdate replaces the check and the install for one test.
func fakeUpdate(t *testing.T, up *selfupdate.Update, checkErr, installErr error) *int {
	t.Helper()
	installs := 0
	oldCheck, oldInstall := checkForUpdate, installUpdate
	t.Cleanup(func() { checkForUpdate, installUpdate = oldCheck, oldInstall })
	checkForUpdate = func(*cli, context.Context) (*selfupdate.Update, error) { return up, checkErr }
	installUpdate = func(_ context.Context, up *selfupdate.Update, progress func(int64, int64)) error {
		installs++
		progress(up.Size, up.Size)
		return installErr
	}
	return &installs
}

func TestUpdateDevBuild(t *testing.T) {
	h := newHarness(t)
	r := h.want(h.run("update", "--check"), 1)
	if !strings.Contains(r.errOut, "not a release") {
		t.Fatalf("stderr: %q", r.errOut)
	}
}

func TestUpdateCheckAndInstall(t *testing.T) {
	h := newHarness(t)
	installs := fakeUpdate(t, nil, nil, nil)
	if r := h.want(h.run("update"), 0); !strings.Contains(r.out, "is the latest version") || *installs != 0 {
		t.Fatalf("no update: %q, %d installs", r.out, *installs)
	}
	if e := jsonLines(t, h.want(h.run("--json", "update", "--check"), 0).out)[0]; e["type"] != "update" || e["available"] != false {
		t.Fatalf("json: %v", e)
	}

	installs = fakeUpdate(t, &selfupdate.Update{Version: "0.7.1", Size: 2 << 20, Delta: true, From: selfupdate.SourceMirror}, nil, nil)
	r := h.want(h.run("update", "--check"), 0)
	if !strings.Contains(r.out, "Pail 0.7.1 is available") || !strings.Contains(r.out, "2.0 MiB, delta update from pail.download") || *installs != 0 {
		t.Fatalf("--check: %q, %d installs", r.out, *installs)
	}
	r = h.want(h.run("update"), 0)
	if *installs != 1 || !strings.Contains(r.out, "0.7.1") {
		t.Fatalf("update: %q, %d installs", r.out, *installs)
	}
	ev := jsonLines(t, h.want(h.run("--json", "update"), 0).out)
	if ev[0]["version"] != "0.7.1" || ev[0]["delta"] != true || ev[0]["source"] != "mirror" || lastSummary(t, h.run("--json", "update").out)["ok"] != true {
		t.Fatalf("json: %v", ev)
	}

	fakeUpdate(t, &selfupdate.Update{Version: "0.7.1", Size: 1}, nil, errors.New("signature does not match"))
	if r := h.want(h.run("update"), 1); !strings.Contains(r.errOut, "signature does not match") {
		t.Fatalf("stderr: %q", r.errOut)
	}
	fakeUpdate(t, nil, errors.New("no route to host"), nil)
	h.want(h.run("update", "--check"), 1)
	h.want(h.run("update", "now"), 2)
}

func TestUpdateSettingKeys(t *testing.T) {
	h := newHarness(t)
	if r := h.want(h.run("settings", "get", "update-check"), 0); r.out != "manual\n" {
		t.Fatalf("default policy: %q", r.out)
	}
	if r := h.want(h.run("settings", "get", "update-source"), 0); r.out != "github\n" {
		t.Fatalf("default source: %q", r.out)
	}
	h.want(h.run("settings", "set", "update-check", "weekly"), 0)
	h.want(h.run("settings", "set", "update-source", "mirror"), 0)
	if r := h.run("settings", "get", "update-check"); r.out != "weekly\n" {
		t.Fatalf("policy: %q", r.out)
	}
	if r := h.run("settings", "get", "update-source"); r.out != "mirror\n" {
		t.Fatalf("source: %q", r.out)
	}
	h.want(h.run("settings", "set", "update-check", "hourly"), 2)
	h.want(h.run("settings", "set", "update-source", "ftp"), 2)
}
