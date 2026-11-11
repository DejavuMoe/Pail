package main

import "testing"

func TestRestoreFromArchiveDialog(t *testing.T) {
	h := newHarness(t, "light")
	if !h.env.Srv.SetStorageClass(bkt, "README.txt", "GLACIER") {
		t.Fatal("no README.txt in the demo bucket")
	}
	h.a.reload()
	h.settle()
	h.mustClick("README.txt")
	h.a.run("restorearchive", "test")
	h.settle()
	h.mustHave("Restore from archive", "Days available", "Retrieval speed", "Expedited")
	h.a.submitArchiveRestore()
	h.waitToast("Restore requested for 1 object")
	if h.a.objOps.ar.open {
		t.Error("the dialog stays open after the request")
	}
}
