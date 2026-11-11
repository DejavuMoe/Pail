package main

import (
	"errors"
	"testing"

	"github.com/DejavuMoe/Pail/internal/cloud"
)

// The background poll of the invalidations showed the spinner and disabled
// Refresh every time it ran; only a refresh the user asks for shows loading.
func TestF13SilentPollShowsNoLoading(t *testing.T) {
	h := newHarness(t, "light")
	h.a.tool = "cloudfront"
	cf := &h.a.tl.cf
	*cf = cfState{api: &controlledCF{}, acct: "A", invFor: "dist-A", invs: []cloud.Invalidation{{ID: "inv-A", Status: "InProgress"}}}
	h.a.cfTick()
	if cf.invLoad {
		t.Fatal("a background poll shows loading and disables Refresh")
	}
	poll := nextCompletion(t, h.a)
	gen := cf.invGen
	// Still one poll at a time.
	h.a.cfTick()
	if cf.invGen != gen {
		t.Fatal("a second poll started while one was in flight")
	}
	// Refresh pressed meanwhile: the request in flight is now the user's, so
	// loading shows until it lands, without a second request.
	h.a.cfLoadInvs(false)
	if !cf.invLoad || cf.invGen != gen {
		t.Fatalf("user refresh during a poll: invLoad=%v gen=%d want %d", cf.invLoad, cf.invGen, gen)
	}
	poll()
	if cf.invLoad || cf.invs[0].Status != "Completed" {
		t.Fatalf("poll did not finish: invLoad=%v invs=%+v", cf.invLoad, cf.invs)
	}
	// A refresh the user asks for shows loading on its own as well.
	h.a.cfLoadInvs(false)
	if !cf.invLoad {
		t.Fatal("a user refresh shows no loading")
	}
	nextCompletion(t, h.a)()
	if cf.invLoad {
		t.Fatal("loading stayed after the refresh")
	}
}

// A failed background poll stays silent, but one the user joined reports.
func TestF13SilentPollErrorReportsOnlyWhenAsked(t *testing.T) {
	h := newHarness(t, "light")
	h.a.tool = "cloudfront"
	cf := &h.a.tl.cf
	*cf = cfState{api: &controlledCF{invErr: errors.New("poll denied")}, acct: "A", invFor: "dist-A", invs: []cloud.Invalidation{{ID: "inv-A", Status: "InProgress"}}}
	h.a.cfTick()
	nextCompletion(t, h.a)()
	if cf.invErr != "" || cf.invLoad {
		t.Fatalf("silent poll failure shown: err=%q load=%v", cf.invErr, cf.invLoad)
	}
	h.a.cfTick()
	poll := nextCompletion(t, h.a)
	h.a.cfLoadInvs(false)
	poll()
	if cf.invErr == "" || cf.invLoad {
		t.Fatalf("refresh the user asked for hid its failure: err=%q load=%v", cf.invErr, cf.invLoad)
	}
}
