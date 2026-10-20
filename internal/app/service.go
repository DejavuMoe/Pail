// Package app is the application layer of Pail. The desktop UI and the CLI
// depend only on this package (docs/rewrite/APP_API.md).
//
// Concurrency rules, which every file in this package follows:
//
//   - Service.mu protects in-memory state only (maps, slices, settings).
//     Keyring, network and disk I/O never happen while it is held.
//   - Writer locks serialise writers of persisted configuration and may be held
//     across I/O; readers (Accounts, Settings, Tasks ...) never take them, so a
//     keyring dialog that blocks a writer cannot freeze the UI:
//     acctMu  SaveAccount / DeleteAccount (keyring + accounts.json)
//     refMu   SetSecret / DeleteSecret (keyring + secret-refs.json)
//     wmu     file-only writers: SaveSettings, SaveUploadRules, SaveMIMETypes,
//     SaveSyncJob ... Never hold wmu across a keyring call, so that the
//     settings dialog cannot be blocked by an account dialog.
//   - The task queue has its own lock (queue.go), also never held across I/O.
//
// Lock order: acctMu/refMu/wmu (independent, never nested) -> mu -> queue lock
// -> event bus lock. Never the reverse. The only exceptions are SetSecretBackend
// and ResetVault, which hold acctMu, then refMu, and take wmu (then backendMu)
// only around the settings.json write, never across a keychain call.
// backendMu (secretbackend.go) is taken shared around every secret read/write.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DejavuMoe/Pail/internal/rules"
)

// Service is the application facade. Create it with New; it is safe for
// concurrent use.
type Service struct {
	dir string
	// kr is what the package reads and writes secrets through: a secretStore
	// that forwards to osKr (the OS keychain) or vlt (the encrypted vault)
	// according to Settings.SecretBackend. See secretbackend.go.
	kr   Keyring
	osKr Keyring
	vlt  *vault
	now  func() time.Time

	// backendMu is held shared around every backend call and exclusively only
	// while the setting that selects the backend changes.
	backendMu sync.RWMutex
	// vaultLast is the UnixNano of the last vault activity (0 = locked);
	// vaultStop ends the idle watcher goroutine.
	vaultLast     atomic.Int64
	vaultStop     chan struct{}
	vaultStopOnce sync.Once

	// Writer locks; see the package comment.
	acctMu, refMu, wmu sync.Mutex

	// mu guards everything below up to the queue block.
	mu       sync.Mutex
	settings Settings
	accounts []accountRecord
	refs     []string // secret reference names (secret-refs.json)
	// acctEpoch increases whenever an account is saved or deleted; the client
	// cache uses it to discard entries built from stale data.
	acctEpoch map[string]uint64
	clients   map[string]*clientEntry
	building  map[string]*buildCall
	// bucketRegion caches "account/bucket" -> region learnt from ListBuckets.
	bucketRegion map[string]string
	// condUnsupported marks accounts whose provider rejected a conditional
	// write header (If-None-Match / If-Match); see caps.go.
	condUnsupported map[string]bool

	// Subsystems implemented in other files of this package. They are loaded
	// by New so that a corrupt file fails startup; the owning files add the
	// methods. All are guarded by mu and persisted under wmu.
	rules    []rules.UploadRule // upload-rules.json
	mime     map[string]string  // mime-types.json (extension -> Content-Type)
	syncJobs []SyncJob          // sync-jobs.json

	q queueState

	// Diagnostic log (logging.go); lgMu serialises open/close only.
	lg   lgHolder
	lgMu sync.Mutex
}

// New opens (or initialises) the configuration directory, loads all
// persisted files and starts the task queue. A corrupt file is an error; it is
// never silently reset.
func New(opts Options) (*Service, error) {
	dir := opts.ConfigDir
	var dirMig configDirMigration
	if dir == "" {
		base, err := userConfigDir()
		if err != nil {
			return nil, fmt.Errorf("locate user config directory: %w", err)
		}
		dir, dirMig = resolveDefaultConfigDir(base)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	s := &Service{
		dir:          dir,
		now:          opts.Now,
		vaultStop:    make(chan struct{}),
		acctEpoch:    map[string]uint64{},
		clients:      map[string]*clientEntry{},
		building:     map[string]*buildCall{},
		bucketRegion: map[string]string{},

		condUnsupported: map[string]bool{},
	}
	osk := opts.Keyring
	migrateKeyring := osk == nil // only the real OS keychain has legacy entries
	if osk == nil {
		osk = osKeyring{keyringService}
	}
	s.osKr = guardedKeyring{osk}
	s.kr = secretStore{s}
	s.vlt = newVault(s.path(vaultFileName)) // always starts locked
	if s.now == nil {
		s.now = time.Now
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	go s.vaultWatch()
	s.initQueue(s.settings.Limits)
	os.MkdirAll(s.LogDir(), 0o700)
	if s.settings.LogToFile {
		s.applyLogging(true)
	}
	s.lgInfo("service start", "log_dir", s.LogDir())
	if dirMig.Note != "" {
		s.lgInfo(dirMig.Note)
	}
	if dirMig.Err != nil {
		s.lgWarn(dirMig.Err.Error())
	}
	if migrateKeyring {
		s.runLegacyKeyringMigration()
	}
	// Compressed-upload spool files orphaned by a crash (never swept before).
	sweepSpoolFiles(os.TempDir(), 24*time.Hour, time.Now())
	return s, nil
}

func (s *Service) path(name string) string { return filepath.Join(s.dir, name) }

// load reads every persisted file. Missing files mean defaults.
func (s *Service) load() error {
	st := DefaultSettings()
	if found, err := readJSON(s.path("settings.json"), &st); err != nil {
		return err
	} else if found {
		st = st.withDefaults()
		if err := st.validate(); err != nil {
			return fmt.Errorf("settings.json is invalid (fix or remove it): %w", err)
		}
	}
	s.settings = st

	var recs []accountRecord
	if _, err := readJSON(s.path("accounts.json"), &recs); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, r := range recs {
		if r.ID == "" || seen[r.ID] {
			return errors.New("accounts.json is corrupt: missing or duplicate account id")
		}
		seen[r.ID] = true
	}
	s.accounts = recs

	if _, err := readJSON(s.path(secretRefsFile), &s.refs); err != nil {
		return err
	}
	if _, err := readJSON(s.path("upload-rules.json"), &s.rules); err != nil {
		return err
	}
	if err := rules.Validate(s.rules); err != nil {
		return fmt.Errorf("upload-rules.json is invalid (fix or remove it): %w", err)
	}
	s.mime = map[string]string{}
	if _, err := readJSON(s.path("mime-types.json"), &s.mime); err != nil {
		return err
	}
	if s.mime == nil {
		s.mime = map[string]string{}
	}
	if _, err := readJSON(s.path("sync-jobs.json"), &s.syncJobs); err != nil {
		return err
	}
	return nil
}

// Shutdown cancels every queued and running task and waits for the running
// ones to return, up to ctx. After Shutdown new tasks are registered as
// Canceled. It is safe to call more than once.
func (s *Service) Shutdown(ctx context.Context) error {
	err := s.shutdownQueue(ctx)
	s.vaultStopOnce.Do(func() { close(s.vaultStop) })
	s.vlt.lock()
	s.closeLogging()
	return err
}
