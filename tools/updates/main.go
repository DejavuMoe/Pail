// Command updates signs the update files of a release and mirrors the latest
// release on pail.download.
//
//	go run ./tools/updates sign   -dir dist -version 0.7.3   # PAIL_UPDATE_KEY
//	go run ./tools/updates verify -dir dist -version 0.7.3
//	go run ./tools/updates mirror -version 0.7.3 -out public -cache /cache
//
// sign runs in the release workflow, after tools/release built the update
// archives (pail-update-<version>-<target>.tar.gz). Per target it signs the
// archive with the private key in PAIL_UPDATE_KEY, which must be the half of
// internal/selfupdate.PublicKey, makes delta updates from the three latest
// earlier stable versions, whose archives it downloads through the published
// manifests, and writes the manifest update-<target>.json, whose URLs are those
// of the release on GitHub. verify checks that with the public key alone.
//
// mirror runs in the website build: it downloads the user archives, the
// SHA256SUMS and the update files of a release from GitHub, checks them, and
// writes them under <out>/download/ and <out>/update/, with the manifests
// pointing at the mirror.
package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/internal/selfupdate"
	"github.com/DejavuMoe/Pail/internal/updatefmt"
)

const allTargets = "windows-amd64,windows-arm64,linux-amd64,linux-arm64,darwin-amd64,darwin-arm64"

// maxDeltas is how many earlier versions get a delta update.
const maxDeltas = 3

var versionRE = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	fl := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	dir := fl.String("dir", "", "release directory (sign, verify)")
	version := fl.String("version", "", "version of the release, without v")
	targets := fl.String("targets", allTargets, "comma-separated targets")
	feed := fl.String("feed", selfupdate.GitHubLatest, "where the published manifests are (sign)")
	release := fl.String("release-url", "", "where the files of the release are (default: its GitHub release)")
	out := fl.String("out", "", "website directory to write download/ and update/ into (mirror)")
	cache := fl.String("cache", "", "directory that keeps the downloaded release between builds (mirror)")
	mirrorURL := fl.String("update-url", selfupdate.MirrorBase, "where the mirror serves update/ (mirror)")
	fl.Parse(os.Args[2:])
	if !versionRE.MatchString(*version) {
		fatal(fmt.Errorf("invalid -version %q", *version))
	}
	if *release == "" {
		*release = selfupdate.GitHubRelease(*version)
	}
	ts := strings.Split(*targets, ",")
	var err error
	switch os.Args[1] {
	case "sign":
		var key ed25519.PrivateKey
		if key, err = signingKey(os.Getenv("PAIL_UPDATE_KEY")); err == nil {
			err = sign(*dir, *version, ts, key, *feed, *release)
		}
	case "verify":
		err = verify(*dir, *version, ts, selfupdate.PublicKey, *release)
	case "mirror":
		err = mirror(*version, ts, *release, *cache, *out, *mirrorURL, selfupdate.PublicKey)
	default:
		usage()
	}
	if err != nil {
		fatal(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: updates sign|verify|mirror -version X [flags]")
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "updates:", err)
	os.Exit(1)
}

// signingKey parses the private key and checks that it signs for the public
// key the programs have.
func signingKey(s string) (ed25519.PrivateKey, error) {
	if strings.TrimSpace(s) == "" {
		return nil, errors.New("PAIL_UPDATE_KEY is not set")
	}
	key, err := updatefmt.ParsePrivateKey(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("PAIL_UPDATE_KEY: %w", err)
	}
	if base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)) != selfupdate.PublicKey {
		return nil, errors.New("PAIL_UPDATE_KEY is not the key of internal/selfupdate.PublicKey")
	}
	return key, nil
}

// ---- sign --------------------------------------------------------------

func sign(dir, version string, targets []string, key ed25519.PrivateKey, feed, release string) error {
	if dir == "" {
		return errors.New("-dir is required")
	}
	pub := key.Public().(ed25519.PublicKey)
	work, err := os.MkdirTemp("", "pail-updates-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	for _, target := range targets {
		if err := signTarget(dir, version, target, key, pub, feed, release, filepath.Join(work, target)); err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
	}
	return nil
}

func signTarget(dir, version, target string, key ed25519.PrivateKey, pub ed25519.PublicKey, feed, release, work string) error {
	name := selfupdate.ArchiveName(version, target)
	archive := filepath.Join(dir, name)
	size, sum, err := hashFile(archive)
	if err != nil {
		return err
	}
	m := updatefmt.Manifest{
		Version:   version,
		Date:      time.Now().UTC().Format(time.RFC3339),
		URL:       release + name,
		Size:      size,
		Signature: updatefmt.Sign(key, sum),
	}

	published, err := fetchManifest(feed + selfupdate.ManifestName(target))
	switch {
	case errors.Is(err, errNotFound):
		fmt.Printf("%s: no delta updates, as no version is published yet\n", target)
	case err != nil:
		return fmt.Errorf("reading the published manifest: %w", err)
	default:
		var bases []updatefmt.Archive
		all := append([]updatefmt.Archive{{Version: published.Version, URL: published.URL, Size: published.Size, Signature: published.Signature}}, published.Previous...)
		for _, a := range all {
			if !strings.ContainsAny(a.Version, "-+") && updatefmt.Compare(a.Version, version) < 0 &&
				!slices.ContainsFunc(bases, func(b updatefmt.Archive) bool { return b.Version == a.Version }) {
				bases = append(bases, a)
			}
		}
		bases = bases[:min(len(bases), maxDeltas)]
		m.Previous = bases[:min(len(bases), maxDeltas-1)]
		if len(bases) > 0 {
			newDir := filepath.Join(work, "new")
			if err := extract(archive, newDir); err != nil {
				return err
			}
			for i, base := range bases {
				d, err := writeDelta(dir, version, target, key, pub, base, newDir, filepath.Join(work, fmt.Sprint(i)), release)
				if err != nil {
					return fmt.Errorf("the delta update from %s: %w", base.Version, err)
				}
				if d.Size >= m.Size {
					os.Remove(filepath.Join(dir, selfupdate.DeltaName(base.Version, version, target)))
					fmt.Printf("%s: no delta update from %s, as it is not smaller than the archive\n", target, base.Version)
					continue
				}
				m.Deltas = append(m.Deltas, d)
				fmt.Printf("%s: delta update from %s: %d bytes instead of %d\n", target, base.Version, d.Size, m.Size)
			}
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, selfupdate.ManifestName(target)), append(b, '\n'), 0o644)
}

// writeDelta downloads the archive of base, checks its signature, writes the
// delta from it to the app in newDir and checks that applying the delta makes
// that app.
func writeDelta(dir, version, target string, key ed25519.PrivateKey, pub ed25519.PublicKey, base updatefmt.Archive, newDir, work, release string) (updatefmt.Delta, error) {
	if err := os.MkdirAll(work, 0o755); err != nil {
		return updatefmt.Delta{}, err
	}
	archive := filepath.Join(work, "base.tar.gz")
	if err := download(base.URL, archive, base.Size, base.Signature, pub); err != nil {
		return updatefmt.Delta{}, err
	}
	oldDir := filepath.Join(work, "old")
	if err := extract(archive, oldDir); err != nil {
		return updatefmt.Delta{}, err
	}
	// The app as a delta has it: inside the bundle on macOS.
	oldApp, newApp := oldDir, newDir
	var entries []string
	if strings.HasPrefix(target, "darwin-") {
		oldApp, newApp = filepath.Join(oldDir, "Pail.app"), filepath.Join(newDir, "Pail.app")
	} else {
		es, err := os.ReadDir(newDir)
		if err != nil {
			return updatefmt.Delta{}, err
		}
		for _, e := range es {
			entries = append(entries, e.Name())
		}
	}
	name := selfupdate.DeltaName(base.Version, version, target)
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		return updatefmt.Delta{}, err
	}
	err = updatefmt.WriteDelta(f, base.Version, version, oldApp, newApp, entries)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(p)
		return updatefmt.Delta{}, err
	}
	size, sum, err := hashFile(p)
	if err != nil {
		return updatefmt.Delta{}, err
	}
	// Apply it as an installation would.
	check := filepath.Join(work, "check")
	if err := applyDelta(p, size, base.Version, version, oldApp, check); err != nil {
		os.Remove(p)
		return updatefmt.Delta{}, fmt.Errorf("applying it: %w", err)
	}
	if err := sameTree(newApp, check); err != nil {
		os.Remove(p)
		return updatefmt.Delta{}, fmt.Errorf("applying it does not make the new version: %w", err)
	}
	return updatefmt.Delta{From: base.Version, URL: release + name, Size: size, Signature: updatefmt.Sign(key, sum)}, nil
}

func applyDelta(p string, size int64, from, to, oldApp, newApp string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return updatefmt.ApplyDelta(f, size, from, to, oldApp, newApp)
}

// sameTree reports how the files of two directories differ.
func sameTree(a, b string) error {
	list := func(root string) (map[string]string, error) {
		files := map[string]string{}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			info, err := d.Info()
			if err != nil {
				return err
			}
			desc := ""
			if runtime.GOOS != "windows" {
				desc = info.Mode().String()
			}
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				l, err := os.Readlink(p)
				if err != nil {
					return err
				}
				desc += " -> " + l
			case info.Mode().IsRegular():
				_, sum, err := hashFile(p)
				if err != nil {
					return err
				}
				desc += " " + hex.EncodeToString(sum)
			}
			files[filepath.ToSlash(rel)] = desc
			return nil
		})
		return files, err
	}
	fa, err := list(a)
	if err != nil {
		return err
	}
	fb, err := list(b)
	if err != nil {
		return err
	}
	for name, desc := range fa {
		if fb[name] != desc {
			return fmt.Errorf("%s differs", name)
		}
	}
	for name := range fb {
		if _, ok := fa[name]; !ok {
			return fmt.Errorf("%s is extra", name)
		}
	}
	return nil
}

// ---- verify ------------------------------------------------------------

// verify checks the signed update files of a release in dir: every manifest
// names files of dir with their sizes and valid signatures, and dir holds no
// other update files.
func verify(dir, version string, targets []string, publicKey, release string) error {
	if dir == "" {
		return errors.New("-dir is required")
	}
	pub, err := updatefmt.ParsePublicKey(publicKey)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, target := range targets {
		m, err := readManifest(filepath.Join(dir, selfupdate.ManifestName(target)))
		if err != nil {
			return err
		}
		known[selfupdate.ManifestName(target)] = true
		if m.Version != version {
			return fmt.Errorf("%s: version %q, want %q", target, m.Version, version)
		}
		check := func(name, url string, size int64, signature string) error {
			if url != release+name {
				return fmt.Errorf("%s: URL %s, want %s", target, url, release+name)
			}
			known[name] = true
			return checkFile(filepath.Join(dir, name), size, signature, pub)
		}
		if err := check(selfupdate.ArchiveName(version, target), m.URL, m.Size, m.Signature); err != nil {
			return err
		}
		for _, d := range m.Deltas {
			if err := check(selfupdate.DeltaName(d.From, version, target), d.URL, d.Size, d.Signature); err != nil {
				return err
			}
			if d.Size >= m.Size {
				return fmt.Errorf("%s: the delta from %s is not smaller than the archive", target, d.From)
			}
		}
		if len(m.Deltas) > maxDeltas || len(m.Previous) > maxDeltas-1 {
			return fmt.Errorf("%s: too many deltas or previous versions", target)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		n := e.Name()
		if (strings.HasPrefix(n, "pail-update-") || strings.HasPrefix(n, "update-")) && !known[n] {
			return fmt.Errorf("unexpected update file %s", n)
		}
	}
	fmt.Println("Verified the signed update files of", version)
	return nil
}

func readManifest(p string) (*updatefmt.Manifest, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m updatefmt.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(p), err)
	}
	return &m, nil
}

func checkFile(p string, size int64, signature string, pub ed25519.PublicKey) error {
	n, sum, err := hashFile(p)
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("%s: %d bytes, the manifest says %d", filepath.Base(p), n, size)
	}
	if err := updatefmt.Verify(pub, sum, signature); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(p), err)
	}
	return nil
}

// ---- mirror ------------------------------------------------------------

// mirror writes the files of release version into out/download (the user
// archives and SHA256SUMS) and out/update (the update files, with manifests
// that point at updateURL). It keeps them in cache, and downloads only what is
// not there yet.
func mirror(version string, targets []string, release, cache, out, updateURL, publicKey string) error {
	if out == "" || cache == "" {
		return errors.New("-out and -cache are required")
	}
	if strings.ContainsAny(version, "-+") {
		return fmt.Errorf("%s is a prerelease: the mirror holds the latest stable release", version)
	}
	if !strings.HasPrefix(updateURL, "https://") || !strings.HasSuffix(updateURL, "/") {
		return fmt.Errorf("-update-url %q must be an https URL ending in /", updateURL)
	}
	pub, err := updatefmt.ParsePublicKey(publicKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return err
	}
	files := filepath.Join(cache, "v"+version)
	if _, err := os.Stat(filepath.Join(files, ".complete")); err != nil {
		tmp := files + ".partial"
		if err := os.MkdirAll(tmp, 0o755); err != nil {
			return err
		}
		if err := fetchRelease(tmp, version, targets, release, pub); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, ".complete"), nil, 0o644); err != nil {
			return err
		}
		os.RemoveAll(files)
		if err := os.Rename(tmp, files); err != nil {
			return err
		}
	}
	// Only the latest release is kept.
	old, _ := filepath.Glob(filepath.Join(cache, "v*"))
	for _, p := range old {
		if p != files {
			os.RemoveAll(p)
		}
	}

	download, update := filepath.Join(out, "download"), filepath.Join(out, "update")
	for _, d := range []string{download, update} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(files)
	if err != nil {
		return err
	}
	for _, e := range entries {
		n := e.Name()
		switch {
		case n == ".complete":
		case strings.HasPrefix(n, "update-") && strings.HasSuffix(n, ".json"):
			m, err := readManifest(filepath.Join(files, n))
			if err != nil {
				return err
			}
			m.URL = updateURL + path.Base(m.URL)
			for i := range m.Deltas {
				m.Deltas[i].URL = updateURL + path.Base(m.Deltas[i].URL)
			}
			b, err := json.MarshalIndent(m, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(update, n), append(b, '\n'), 0o644); err != nil {
				return err
			}
		case strings.HasPrefix(n, "pail-update-"):
			if err := linkOrCopy(filepath.Join(files, n), filepath.Join(update, n)); err != nil {
				return err
			}
		default:
			if err := linkOrCopy(filepath.Join(files, n), filepath.Join(download, n)); err != nil {
				return err
			}
		}
	}
	fmt.Println("Mirrored the files of", version)
	return nil
}

// fetchRelease downloads the files of a release into dir and checks them: the
// user archives against SHA256SUMS, the update files against the manifests
// and their signatures. A release made before updates has no manifests.
func fetchRelease(dir, version string, targets []string, release string, pub ed25519.PublicKey) error {
	sumsPath := filepath.Join(dir, "SHA256SUMS")
	if err := download(release+"SHA256SUMS", sumsPath, 0, "", nil); err != nil {
		return err
	}
	f, err := os.Open(sumsPath)
	if err != nil {
		return err
	}
	type entry struct{ sum, name string }
	var sums []entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 || !strings.HasPrefix(name, "Pail-v"+version+"-") || strings.ContainsAny(name, "/\\") {
			f.Close()
			return fmt.Errorf("SHA256SUMS: unexpected line %q", line)
		}
		sums = append(sums, entry{sum, name})
	}
	f.Close()
	if err := sc.Err(); err != nil {
		return err
	}
	if len(sums) != len(targets) {
		return fmt.Errorf("SHA256SUMS lists %d archives, want %d", len(sums), len(targets))
	}
	for _, s := range sums {
		p := filepath.Join(dir, s.name)
		if err := download(release+s.name, p, 0, "", nil); err != nil {
			return err
		}
		_, sum, err := hashFile(p)
		if err != nil {
			return err
		}
		if hex.EncodeToString(sum) != s.sum {
			return fmt.Errorf("%s does not match SHA256SUMS", s.name)
		}
	}

	missing := 0
	for _, target := range targets {
		name := selfupdate.ManifestName(target)
		m, err := fetchManifest(release + name)
		if errors.Is(err, errNotFound) {
			missing++
			continue
		}
		if err != nil {
			return err
		}
		if m.Version != version {
			return fmt.Errorf("%s: version %q, want %q", name, m.Version, version)
		}
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644); err != nil {
			return err
		}
		get := func(want, url string, size int64, signature string) error {
			if path.Base(url) != want {
				return fmt.Errorf("%s names %s, want %s", name, url, want)
			}
			return download(url, filepath.Join(dir, want), size, signature, pub)
		}
		if err := get(selfupdate.ArchiveName(version, target), m.URL, m.Size, m.Signature); err != nil {
			return err
		}
		for _, d := range m.Deltas {
			if err := get(selfupdate.DeltaName(d.From, version, target), d.URL, d.Size, d.Signature); err != nil {
				return err
			}
		}
	}
	if missing != 0 && missing != len(targets) {
		return fmt.Errorf("the release has the manifests of %d of %d targets", len(targets)-missing, len(targets))
	}
	return nil
}

func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// ---- downloads ---------------------------------------------------------

var errNotFound = errors.New("not found")

var client = &http.Client{Timeout: 10 * time.Minute}

// get requests url, retrying server errors; a 404 is errNotFound.
func get(url string) (*http.Response, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://127.0.0.1:") {
		return nil, fmt.Errorf("refusing to download %s: not HTTPS", url)
	}
	var last error
	for attempt := range 4 {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "pail-release-tools")
		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			return resp, nil
		case resp.StatusCode == http.StatusNotFound:
			resp.Body.Close()
			return nil, fmt.Errorf("GET %s: %w", url, errNotFound)
		}
		resp.Body.Close()
		last = fmt.Errorf("GET %s: %s", url, resp.Status)
		if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return nil, last
		}
	}
	return nil, last
}

func fetchManifest(url string) (*updatefmt.Manifest, error) {
	resp, err := get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var m updatefmt.Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, updatefmt.MaxManifestSize)).Decode(&m); err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	if m.Version == "" || m.URL == "" || m.Size <= 0 || m.Signature == "" {
		return nil, fmt.Errorf("%s: incomplete manifest", url)
	}
	return &m, nil
}

// download saves url to p; with a key, it checks the size and the signature.
func download(url, p string, size int64, signature string, pub ed25519.PublicKey) error {
	if pub != nil && (size <= 0 || size > updatefmt.MaxArchiveSize) {
		return fmt.Errorf("%s: invalid size %d", url, size)
	}
	resp, err := get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, updatefmt.MaxArchiveSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	if pub == nil {
		return nil
	}
	if err := checkFile(p, size, signature, pub); err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	return nil
}

func hashFile(p string) (int64, []byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return n, h.Sum(nil), err
}

func extract(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	return updatefmt.ExtractArchive(f, dir)
}
