package integration_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/app"
)

const extMiB = 1 << 20

// TestExtUploadDownloadRoundTrip: single file, special-character keys, folder with
// an empty sub-folder, a 20 MiB multipart object, then downloads with SHA-256 checks.
func TestExtUploadDownloadRoundTrip(t *testing.T) {
	x := newExt(t, "")
	uploadDownloadRoundTrip(t, x.target, 20*extMiB, 4) // exactly four 5 MiB parts
}

// TestExtQuestionMarkKey: a key with "?" (valid in S3, illegal in Windows file names).
func TestExtQuestionMarkKey(t *testing.T) {
	x := newExt(t, "")
	key := "qm/a b+c#?.txt"
	body := []byte("question mark body")
	x.putk(t, key, body)
	pg, err := x.svc.List(x.ctx, x.at("qm/"), app.ListOptions{})
	if err != nil || len(pg.Entries) != 1 || pg.Entries[0].Key != x.k(key) {
		t.Fatalf("List = %+v, %v", pg.Entries, err)
	}
	info, err := x.svc.Head(x.ctx, x.at("qm/"), x.k(key), "")
	if err != nil || info.Size != int64(len(body)) {
		t.Fatalf("Head = %+v, %v", info, err)
	}
	dir := t.TempDir()
	g, err := x.svc.Download(app.DownloadRequest{Source: x.at("qm/"), Keys: []string{x.k(key)}, DestDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	ts, items := x.waitGroup(t, g)
	files := localFiles(t, dir)
	t.Logf("download of %q on %s: tasks=%d files=%v%s", key, runtime.GOOS, len(ts), files, describeTasks(ts, items))
	if runtime.GOOS != "windows" {
		if len(files) != 1 || sum(body) != files["a b+c#?.txt"] {
			t.Errorf("on %s the key must download under its exact name: %v", runtime.GOOS, files)
		}
		return
	}
	// Windows cannot hold the name: either a sanitised file with the right bytes, or a
	// failed item with an explanation; never a missing file reported as success.
	switch len(files) {
	case 1:
		for p, s := range files {
			if s != sum(body) {
				t.Errorf("sanitised file %q has wrong content", p)
			}
		}
	case 0:
		if countStatus(items, app.ItemFailed) == 0 || items[0].Err == "" {
			t.Errorf("no file and no explained failure:%s", describeTasks(ts, items))
		}
	default:
		t.Errorf("unexpected files: %v", files)
	}
}

func TestExtUploadConflictPolicies(t *testing.T) {
	x := newExt(t, "")
	uploadConflictPolicies(t, x.target)
}

func TestExtDownloadConflictPolicies(t *testing.T) {
	x := newExt(t, "")
	downloadConflictPolicies(t, x.target)
}

// TestExtCreateOnlyRace records whether the server honours If-None-Match: * on
// CompleteMultipartUpload and then runs the race of the MinIO suite: a second client
// writes the destination while the app's multipart upload is in flight.
func TestExtCreateOnlyRace(t *testing.T) {
	x := newExt(t, "")
	s := x.sdk.S3()

	// Probe with raw calls: one tiny part, a racer, then a create-only completion.
	probeKey := x.k("race/probe.bin")
	cmu, err := s.CreateMultipartUpload(x.ctx, &s3.CreateMultipartUploadInput{Bucket: &x.bucket, Key: &probeKey})
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	up, err := s.UploadPart(x.ctx, &s3.UploadPartInput{Bucket: &x.bucket, Key: &probeKey, UploadId: cmu.UploadId, PartNumber: aws.Int32(1), Body: strings.NewReader("part-one")})
	if err != nil {
		t.Fatalf("UploadPart: %v", err)
	}
	x.putk(t, "race/probe.bin", []byte("racer"))
	_, cerr := s.CompleteMultipartUpload(x.ctx, &s3.CompleteMultipartUploadInput{
		Bucket: &x.bucket, Key: &probeKey, UploadId: cmu.UploadId, IfNoneMatch: aws.String("*"),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{ETag: up.ETag, PartNumber: aws.Int32(1)}}},
	})
	after := string(x.get(t, x.bucket, probeKey, ""))
	honoured := false
	switch {
	case cerr == nil && after == "part-one":
		t.Logf("RESULT: CompleteMultipartUpload ignores If-None-Match:* (the racer was overwritten)")
	case cerr != nil && after == "racer":
		honoured = true
		t.Logf("RESULT: CompleteMultipartUpload honours If-None-Match:* (%s); the racer survived", errCode(cerr))
	default:
		t.Logf("RESULT: inconclusive: err=%v body=%q", cerr, after)
	}
	if cerr != nil {
		_, _ = s.AbortMultipartUpload(x.ctx, &s3.AbortMultipartUploadInput{Bucket: &x.bucket, Key: &probeKey, UploadId: cmu.UploadId})
	}

	createOnlyRace(t, x.target, 16*extMiB, honoured) // four parts: a window between the HEAD and the commit
}

// TestExtChecksumModes uploads small and multipart payloads through accounts with every
// ChecksumMode and checks that the server accepts them and the bytes survive.
func TestExtChecksumModes(t *testing.T) {
	extLoad(t) // skip early without environment
	small, big := []byte("checksum compatibility"), payload(3, 6*extMiB)
	for _, mode := range []string{"", "auto", "when_required", "when_supported"} {
		name := mode
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			x := newExt(t, mode)
			t.Logf("ChecksumMode %q", mode)
			checksumModeRoundTrip(t, x.target, small, big)
		})
	}
}

// TestExtServerChecksums records which explicit checksum algorithms the server accepts
// on PutObject (raw SDK calls below the run prefix).
func TestExtServerChecksums(t *testing.T) {
	x := newExt(t, "when_supported")
	s := x.sdk.S3()
	for _, algo := range []types.ChecksumAlgorithm{types.ChecksumAlgorithmCrc32, types.ChecksumAlgorithmCrc32c, types.ChecksumAlgorithmSha1, types.ChecksumAlgorithmSha256, types.ChecksumAlgorithmCrc64nvme} {
		key := x.k("sum/" + string(algo) + ".txt")
		out, err := s.PutObject(x.ctx, &s3.PutObjectInput{Bucket: &x.bucket, Key: &key, Body: strings.NewReader("checksummed body"), ChecksumAlgorithm: algo})
		if err != nil {
			t.Logf("PutObject with ChecksumAlgorithm=%s: %s (%v)", algo, errCode(err), err)
			continue
		}
		t.Logf("PutObject with ChecksumAlgorithm=%s: accepted; response checksum crc32=%q crc32c=%q sha1=%q sha256=%q crc64nvme=%q", algo,
			aws.ToString(out.ChecksumCRC32), aws.ToString(out.ChecksumCRC32C), aws.ToString(out.ChecksumSHA1), aws.ToString(out.ChecksumSHA256), aws.ToString(out.ChecksumCRC64NVME))
		g, err := s.GetObject(x.ctx, &s3.GetObjectInput{Bucket: &x.bucket, Key: &key, ChecksumMode: types.ChecksumModeEnabled})
		if err != nil {
			t.Errorf("GetObject with checksum mode on: %v", err)
			continue
		}
		g.Body.Close()
	}
	// A wrong checksum must be refused (integrity actually checked).
	key := x.k("sum/bad.txt")
	_, err := s.PutObject(x.ctx, &s3.PutObjectInput{Bucket: &x.bucket, Key: &key, Body: strings.NewReader("checksummed body"), ChecksumSHA256: aws.String("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")})
	t.Logf("PutObject with a wrong ChecksumSHA256: %v", map[bool]string{true: "REFUSED (" + errCode(err) + ")", false: "ACCEPTED (integrity not verified)"}[err != nil])
}
