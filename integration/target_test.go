package integration_test

import (
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/app"
)

// target is one backend a shared scenario runs against: the app service of an
// appEnv plus the bucket and key prefix every key of the scenario lives below.
// On MinIO (TestApp*) the test owns a fresh bucket and the prefix is empty; on
// the external endpoint (TestExt*) the bucket is shared and the prefix is the
// test's own folder below the run prefix, so scenarios must address keys only
// through k, at, pick and putk, and touch the whole bucket only when ownsBucket.
type target struct {
	*appEnv
	bucket string
	prefix string

	// ownsBucket: the test created the bucket, so whole-bucket operations are safe.
	ownsBucket bool
	// external: a real server; data volume counts against the run budget, and the
	// scenario records server limitations instead of asserting MinIO's behaviour.
	external bool
}

// target creates a test-owned bucket (see appEnv.bucket) and returns it as a target.
func (a *appEnv) target(t *testing.T, versioned bool) *target {
	t.Helper()
	return &target{appEnv: a, bucket: a.bucket(t, versioned), ownsBucket: true}
}

// sub is the same backend rooted at the folder rel of this target.
func (tg *target) sub(rel string) *target {
	s := *tg
	s.prefix = tg.prefix + rel
	s.ownsBucket = false
	if s.external {
		extGuardPrefix(s.prefix)
	}
	return &s
}

// k is the full key for a path relative to the target prefix.
func (tg *target) k(rel string) string { return tg.prefix + rel }

// at is the Location of a folder relative to the target prefix ("" = the target root).
func (tg *target) at(rel string) app.Location { return tg.loc(tg.bucket, tg.prefix+rel) }

// pick is a Selection of keys relative to the target prefix, rooted at folder rel.
func (tg *target) pick(rel string, keys ...string) app.Selection {
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = tg.prefix + k
	}
	return app.Selection{Location: tg.at(rel), Keys: full}
}

// putk uploads through the independent SDK client below the target prefix.
func (tg *target) putk(t *testing.T, rel string, data []byte) string {
	t.Helper()
	tg.spend(t, int64(len(data)))
	return tg.put(t, tg.bucket, tg.k(rel), data)
}

// getk reads (the current version of) a key relative to the target prefix.
func (tg *target) getk(t *testing.T, rel string) []byte {
	t.Helper()
	return tg.get(t, tg.bucket, tg.k(rel), "")
}

// existsk reports whether a key relative to the target prefix exists.
func (tg *target) existsk(rel string) bool { return tg.exists(tg.bucket, tg.k(rel)) }

// spend accounts data volume against the per-run budget of the external endpoint.
func (tg *target) spend(t *testing.T, n int64) {
	t.Helper()
	if !tg.external {
		return
	}
	if n > extMaxObject {
		t.Fatalf("object of %d bytes exceeds the %d byte per-object limit", n, extMaxObject)
	}
	if total := extSpent.Add(n); total > extBudgetBytes {
		t.Fatalf("data budget exceeded: %d bytes spent of %d", total, extBudgetBytes)
	}
}

// relKeys lists the keys below prefix+rel, relative to the target prefix.
func (tg *target) relKeys(t *testing.T, rel string) []string {
	t.Helper()
	var out []string
	for _, k := range tg.keys(t, tg.bucket, tg.prefix+rel) {
		out = append(out, strings.TrimPrefix(k, tg.prefix))
	}
	return out
}

func (tg *target) syncJob(dir, rel, direction, mode, compare string) app.SyncJob {
	return app.SyncJob{
		Name: "it", LocalDir: dir, Remote: tg.at(rel), Direction: direction, Mode: mode, Compare: compare,
	}
}

// checkTree verifies that every file of the tree exists below relPrefix with its content.
func (tg *target) checkTree(t *testing.T, relPrefix string, files map[string][]byte) {
	t.Helper()
	for k, v := range files {
		dk := tg.k(relPrefix + k)
		got, err := tg.tryGet(tg.bucket, dk, "")
		if err != nil || sum(got) != sum(v) {
			t.Errorf("%s/%s: missing or wrong content (%v)", tg.bucket, dk, err)
		}
	}
}
