package app

// The encrypted vault: an optional, file-backed Keyring protected by a master
// password (vault.json in the configuration directory).
//
// File format (version 1):
//
//	{"format":"pail-vault" (read: also "opens3-vault"),"version":1,
//	 "kdf":{"name":"argon2id","time":3,"memoryKiB":65536,"threads":4,"salt":b64(16)},
//	 "cipher":"xchacha20poly1305","nonce":b64(24),"data":b64}
//
// data is the XChaCha20-Poly1305 sealing of a JSON object (key -> value) under
// the Argon2id-derived 32-byte key. The associated data is the JSON encoding of
// everything except nonce and data, so the KDF parameters, the salt and the
// algorithm names are authenticated: swapping any of them fails the AEAD.
// Every write uses a fresh random 24-byte nonce.
//
// The derived key lives in memory only while the vault is unlocked; lock wipes
// it and the decrypted values (best effort: Go strings handed out earlier and
// copies inside other components cannot be wiped).
//
// A vault file that cannot be decrypted is never overwritten: every write
// first re-reads the file and re-decrypts it with the held key, and refuses to
// continue when the file changed underneath.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	vaultFileName = "vault.json"
	vaultFormat   = "pail-vault"
	// legacyVaultFormat is the format name that builds before the rename to Pail wrote. Such files are still read (their AAD uses the header as read) and are rewritten as vaultFormat on the next write.
	legacyVaultFormat = "opens3-vault"
	vaultVersion      = 1
	vaultKDFName      = "argon2id"
	vaultCipherName   = "xchacha20poly1305"
	vaultSaltLen      = 16
	vaultKeyLen       = 32
	vaultMaxFile      = 64 << 20 // sanity bound on vault.json

	// Master password policy: at least 8 characters, at most 1024 bytes.
	vaultMinPassword = 8
	vaultMaxPassword = 1024

	// Accepted KDF parameter ranges. A tampered file must not be able to make
	// the process allocate gigabytes or burn minutes of CPU.
	vaultMinMemKiB  = 8 << 10 // 8 MiB
	vaultMaxMemKiB  = 1 << 20 // 1 GiB
	vaultMinTime    = 1
	vaultMaxTime    = 10
	vaultMinThreads = 1
	vaultMaxThreads = 16
)

// vaultKDFParams are the Argon2id cost parameters used for new vaults and
// password changes.
type vaultKDFParams struct {
	Time      uint32
	MemoryKiB uint32
	Threads   uint8
}

// defaultVaultKDF are the production parameters (64 MiB, 3 passes, 4 lanes).
var defaultVaultKDF = vaultKDFParams{Time: 3, MemoryKiB: 64 << 10, Threads: 4}

// vaultNewKDF is what newVault uses; tests lower it to stay fast.
var vaultNewKDF = defaultVaultKDF

type vaultKDF struct {
	Name      string `json:"name"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memoryKiB"`
	Threads   uint8  `json:"threads"`
	Salt      string `json:"salt"`
}

// vaultHeader is the authenticated part of the file; its JSON encoding is the
// AEAD associated data (struct field order makes it canonical).
type vaultHeader struct {
	Format  string   `json:"format"`
	Version int      `json:"version"`
	KDF     vaultKDF `json:"kdf"`
	Cipher  string   `json:"cipher"`
}

type vaultFile struct {
	vaultHeader
	Nonce string `json:"nonce"`
	Data  string `json:"data"`
}

func (h vaultHeader) aad() []byte {
	b, _ := json.Marshal(h) // a struct of strings and numbers cannot fail
	return b
}

var b64 = base64.StdEncoding.Strict()

// vault is the file-backed store. All methods are safe for concurrent use.
type vault struct {
	path string
	kdf  vaultKDFParams

	mu   sync.Mutex
	hdr  vaultHeader       // header of the file this key belongs to
	key  []byte            // nil while locked
	data map[string][]byte // decrypted entries
}

func newVault(path string) *vault { return &vault{path: path, kdf: vaultNewKDF} }

// ---- errors -------------------------------------------------------------------

func lockedErr() *OpError {
	return &OpError{Kind: ErrLocked, Message: lockedMessage, err: ErrVaultLocked}
}

const lockedMessage = "the encrypted vault is locked; enter the master password to unlock it"

func vaultInvalid(format string, a ...any) *OpError {
	return &OpError{Kind: ErrInvalid, Message: fmt.Sprintf(format, a...)}
}

func errWrongPassword() *OpError {
	return &OpError{Kind: ErrAccessDenied, Message: "wrong master password (or the vault file was modified)"}
}

func checkPassword(pw string) error {
	if n := utf8.RuneCountInString(pw); n < vaultMinPassword {
		return vaultInvalid("the master password must have at least %d characters", vaultMinPassword)
	}
	if len(pw) > vaultMaxPassword {
		return vaultInvalid("the master password must not exceed %d bytes", vaultMaxPassword)
	}
	return nil
}

// ---- file I/O -------------------------------------------------------------------

func (v *vault) exists() bool {
	_, err := os.Stat(v.path)
	return err == nil
}

// readFile loads and structurally validates vault.json. It never decrypts.
func (v *vault) readFile() (vaultFile, error) {
	var f vaultFile
	fi, err := os.Stat(v.path)
	if errors.Is(err, os.ErrNotExist) {
		return f, &OpError{Kind: ErrNotFound, Message: "vault.json does not exist"}
	}
	if err != nil {
		return f, err
	}
	if fi.Size() > vaultMaxFile {
		return f, vaultInvalid("vault.json is implausibly large; it was not opened")
	}
	raw, err := os.ReadFile(v.path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, vaultInvalid("vault.json is corrupt (it was not modified): %v", err)
	}
	return f, validateVaultFile(f)
}

func validateVaultFile(f vaultFile) error {
	if f.Format != vaultFormat && f.Format != legacyVaultFormat {
		return vaultInvalid("vault.json is not a Pail vault")
	}
	if f.Version != vaultVersion {
		return vaultInvalid("vault.json has unsupported version %d (this Pail reads version %d)", f.Version, vaultVersion)
	}
	k := f.KDF
	if k.Name != vaultKDFName || f.Cipher != vaultCipherName {
		return vaultInvalid("vault.json uses unsupported algorithms (%s / %s)", k.Name, f.Cipher)
	}
	if k.MemoryKiB < vaultMinMemKiB || k.MemoryKiB > vaultMaxMemKiB ||
		k.Time < vaultMinTime || k.Time > vaultMaxTime ||
		k.Threads < vaultMinThreads || k.Threads > vaultMaxThreads {
		return vaultInvalid("vault.json has key-derivation parameters outside the accepted range; it was not used")
	}
	if s, err := b64.DecodeString(k.Salt); err != nil || len(s) != vaultSaltLen {
		return vaultInvalid("vault.json has an invalid salt")
	}
	if n, err := b64.DecodeString(f.Nonce); err != nil || len(n) != chacha20poly1305.NonceSizeX {
		return vaultInvalid("vault.json has an invalid nonce")
	}
	if d, err := b64.DecodeString(f.Data); err != nil || len(d) < chacha20poly1305.Overhead {
		return vaultInvalid("vault.json has invalid data")
	}
	return nil
}

func deriveKey(pw string, k vaultKDF) []byte {
	salt, _ := b64.DecodeString(k.Salt)
	return argon2.IDKey([]byte(pw), salt, k.Time, k.MemoryKiB, k.Threads, vaultKeyLen)
}

func openFile(f vaultFile, key []byte) (map[string][]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce, _ := b64.DecodeString(f.Nonce)
	ct, _ := b64.DecodeString(f.Data)
	pt, err := aead.Open(nil, nonce, ct, f.vaultHeader.aad())
	if err != nil {
		return nil, errWrongPassword()
	}
	defer wipe(pt)
	var m map[string]string
	if err := json.Unmarshal(pt, &m); err != nil {
		return nil, vaultInvalid("the vault content is corrupt: %v", err)
	}
	out := make(map[string][]byte, len(m))
	for k, val := range m {
		out[k] = []byte(val)
	}
	return out, nil
}

// sealFile encrypts data under key with a fresh nonce. hdr must already be valid.
func sealFile(hdr vaultHeader, key []byte, data map[string][]byte) (vaultFile, error) {
	m := make(map[string]string, len(data))
	for k, val := range data {
		m[k] = string(val)
	}
	pt, err := json.Marshal(m)
	if err != nil {
		return vaultFile{}, err
	}
	defer wipe(pt)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return vaultFile{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return vaultFile{}, err
	}
	ct := aead.Seal(nil, nonce, pt, hdr.aad())
	return vaultFile{vaultHeader: hdr, Nonce: b64.EncodeToString(nonce), Data: b64.EncodeToString(ct)}, nil
}

func (v *vault) newHeader() (vaultHeader, error) {
	salt := make([]byte, vaultSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return vaultHeader{}, err
	}
	return vaultHeader{
		Format: vaultFormat, Version: vaultVersion, Cipher: vaultCipherName,
		KDF: vaultKDF{Name: vaultKDFName, Time: v.kdf.Time, MemoryKiB: v.kdf.MemoryKiB, Threads: v.kdf.Threads, Salt: b64.EncodeToString(salt)},
	}, nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func wipeMap(m map[string][]byte) {
	for _, b := range m {
		wipe(b)
	}
}

// ---- lifecycle --------------------------------------------------------------------

func (v *vault) unlocked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.key != nil
}

// install replaces the in-memory state (caller holds mu).
func (v *vault) installLocked(hdr vaultHeader, key []byte, data map[string][]byte) {
	wipe(v.key)
	wipeMap(v.data)
	v.hdr, v.key, v.data = hdr, key, data
}

// create makes a new, empty vault protected by pw and leaves it unlocked. It
// refuses to replace an existing file.
func (v *vault) create(pw string) error {
	if err := checkPassword(pw); err != nil {
		return err
	}
	if v.exists() {
		return &OpError{Kind: ErrConflict, Message: "a vault already exists"}
	}
	hdr, err := v.newHeader()
	if err != nil {
		return err
	}
	key := deriveKey(pw, hdr.KDF)
	data := map[string][]byte{}
	f, err := sealFile(hdr, key, data)
	if err != nil {
		return err
	}
	if err := writeJSONAtomic(v.path, f); err != nil {
		wipe(key)
		return err
	}
	v.mu.Lock()
	v.installLocked(hdr, key, data)
	v.mu.Unlock()
	return nil
}

// unlock derives the key from pw, decrypts the file and keeps the key. The
// expensive derivation runs outside the lock so concurrent reads (which fail
// as locked) are not delayed.
func (v *vault) unlock(pw string) error {
	if pw == "" {
		return vaultInvalid("a master password is required")
	}
	f, err := v.readFile()
	if err != nil {
		return err
	}
	key := deriveKey(pw, f.KDF)
	data, err := openFile(f, key)
	if err != nil {
		wipe(key)
		return err
	}
	v.mu.Lock()
	v.installLocked(f.vaultHeader, key, data)
	v.mu.Unlock()
	return nil
}

// lock wipes the key and the decrypted entries; it reports whether the vault
// was unlocked.
func (v *vault) lock() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	was := v.key != nil
	v.installLocked(vaultHeader{}, nil, nil)
	return was
}

// destroy locks the vault and removes vault.json.
func (v *vault) destroy() error {
	v.lock()
	if err := os.Remove(v.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	syncDir(filepath.Dir(v.path))
	return nil
}

// changePassword re-encrypts the vault under newPW with a fresh salt. oldPW
// must open the file; the vault is unlocked afterwards.
func (v *vault) changePassword(oldPW, newPW string) error {
	if err := checkPassword(newPW); err != nil {
		return err
	}
	if oldPW == "" {
		return vaultInvalid("the current master password is required")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	f, err := v.readFile()
	if err != nil {
		return err
	}
	oldKey := deriveKey(oldPW, f.KDF)
	data, err := openFile(f, oldKey)
	wipe(oldKey)
	if err != nil {
		return err
	}
	hdr, err := v.newHeader()
	if err != nil {
		wipeMap(data)
		return err
	}
	key := deriveKey(newPW, hdr.KDF)
	nf, err := sealFile(hdr, key, data)
	if err == nil {
		err = writeJSONAtomic(v.path, nf)
	}
	if err != nil {
		wipe(key)
		wipeMap(data)
		return err
	}
	v.installLocked(hdr, key, data)
	return nil
}

// ---- Keyring ------------------------------------------------------------------------

// Get implements Keyring.
func (v *vault) Get(key string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return "", lockedErr()
	}
	b, ok := v.data[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return string(b), nil
}

// Set implements Keyring.
func (v *vault) Set(key, value string) error {
	return v.setMany(map[string]string{key: value})
}

// Delete implements Keyring; deleting an absent entry succeeds.
func (v *vault) Delete(key string) error {
	return v.mutate(func(m map[string][]byte) bool {
		if _, ok := m[key]; !ok {
			return false
		}
		wipe(m[key])
		delete(m, key)
		return true
	})
}

// setMany stores all entries with a single write of the file.
func (v *vault) setMany(entries map[string]string) error {
	return v.mutate(func(m map[string][]byte) bool {
		for k, val := range entries {
			wipe(m[k])
			m[k] = []byte(val)
		}
		return true
	})
}

// deleteMany removes the keys with a single write.
func (v *vault) deleteMany(keys []string) error {
	return v.mutate(func(m map[string][]byte) bool {
		changed := false
		for _, k := range keys {
			if b, ok := m[k]; ok {
				wipe(b)
				delete(m, k)
				changed = true
			}
		}
		return changed
	})
}

// mutate applies fn to the current content of the file and writes the result.
// The file is re-read and decrypted first, so a concurrent change by another
// process is merged rather than lost, and a file that no longer matches the
// unlocked state is never overwritten. fn reports whether it changed anything.
func (v *vault) mutate(fn func(map[string][]byte) bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return lockedErr()
	}
	f, err := v.readFile()
	if err != nil {
		return err
	}
	if string(f.vaultHeader.aad()) != string(v.hdr.aad()) {
		return &OpError{Kind: ErrConflict, Message: "the vault was changed by another process (for example its password); lock and unlock it again"}
	}
	cur, err := openFile(f, v.key)
	if err != nil {
		return vaultInvalid("vault.json no longer matches the unlocked vault; it was not overwritten")
	}
	if !fn(cur) {
		wipeMap(cur)
		return nil
	}
	hdr := v.hdr
	hdr.Format = vaultFormat // a legacy file is upgraded by this write (new AAD, fresh nonce, same key)
	nf, err := sealFile(hdr, v.key, cur)
	if err == nil {
		err = writeJSONAtomic(v.path, nf)
	}
	if err != nil {
		wipeMap(cur)
		return err
	}
	v.hdr = hdr
	wipeMap(v.data)
	v.data = cur
	return nil
}

// verify re-reads vault.json from disk and checks that it holds exactly the
// given values for the given keys (migration read-back).
func (v *vault) verify(want map[string]string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return lockedErr()
	}
	f, err := v.readFile()
	if err != nil {
		return err
	}
	cur, err := openFile(f, v.key)
	if err != nil {
		return err
	}
	defer wipeMap(cur)
	for k, val := range want {
		if got, ok := cur[k]; !ok || subtle.ConstantTimeCompare(got, []byte(val)) != 1 {
			return fmt.Errorf("vault read-back mismatch")
		}
	}
	return nil
}

var _ Keyring = (*vault)(nil)
