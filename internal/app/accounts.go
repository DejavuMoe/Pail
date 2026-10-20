package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/internal/storage"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// accountRecord is the persisted form of an Account (accounts.json). It is the
// Account fields (flat, so the file stays an []Account superset) plus the
// bookkeeping needed to find the keyring entries without asking the keyring:
//
//	SecretGen   generation of the entries "account/<id>/secret[.<gen>]" and
//	            "account/<id>/session[.<gen>]"; a fresh generation is used each
//	            time secrets are replaced (see SaveAccount)
//	HasSecret   a secret access key is stored
//	HasSession  a session token is stored
type accountRecord struct {
	Account
	SecretGen  int  `json:"secretGen,omitempty"`
	HasSecret  bool `json:"hasSecret,omitempty"`
	HasSession bool `json:"hasSession,omitempty"`
}

var accountIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // the OS random source is unavailable; nothing sensible can continue
	}
	return hex.EncodeToString(b[:])
}

// Accounts returns a copy of the saved accounts in creation order. It never
// touches the keyring.
func (s *Service) Accounts() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Account, len(s.accounts))
	for i, r := range s.accounts {
		out[i] = r.Account
		out[i].CDNBuckets = append([]string(nil), r.CDNBuckets...) // nil stays nil
		out[i].SecretMissing = r.Credentials == CredStatic && !r.HasSecret
	}
	return out
}

// account returns the saved account with the given ID.
func (s *Service) account(id string) (accountRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accountLocked(id)
}

func (s *Service) accountLocked(id string) (accountRecord, bool) {
	for _, r := range s.accounts {
		if r.ID == id {
			return r, true
		}
	}
	return accountRecord{}, false
}

// normalizeAccount trims, defaults and validates a. It does not look at secrets.
func normalizeAccount(a Account) (Account, error) {
	a.SecretMissing = false // derived, see Account
	a.ID = strings.TrimSpace(a.ID)
	a.Name = strings.TrimSpace(a.Name)
	a.Endpoint = strings.TrimRight(strings.TrimSpace(a.Endpoint), "/")
	a.Region = strings.TrimSpace(a.Region)
	a.AccessKeyID = strings.TrimSpace(a.AccessKeyID)
	a.Profile = strings.TrimSpace(a.Profile)
	a.RoleARN = strings.TrimSpace(a.RoleARN)
	a.ExternalID = strings.TrimSpace(a.ExternalID)
	a.RoleSessionName = strings.TrimSpace(a.RoleSessionName)

	if a.ID != "" && !accountIDRe.MatchString(a.ID) {
		return a, errors.New("account id may only contain letters, digits, '-' and '_' (max 64)")
	}
	if a.Name == "" {
		return a, errors.New("account name is required")
	}
	if a.Provider == "" {
		if a.Endpoint != "" {
			a.Provider = ProviderS3
		} else {
			a.Provider = ProviderAWS
		}
	}
	switch a.Provider {
	case ProviderAWS, ProviderS3:
	default:
		return a, fmt.Errorf("unknown provider %q", a.Provider)
	}
	if a.Endpoint != "" {
		u, err := url.Parse(a.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") ||
			u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(a.Endpoint, "?#") {
			return a, errors.New("endpoint must be an http(s) URL without credentials, query or fragment")
		}
	} else if a.Provider == ProviderS3 {
		return a, errors.New("an endpoint is required for S3-compatible providers")
	}
	switch a.ChecksumMode {
	case "":
		a.ChecksumMode = storage.ChecksumAuto
	case storage.ChecksumAuto, storage.ChecksumWhenRequired, storage.ChecksumWhenSupported:
	default:
		return a, fmt.Errorf("unknown checksum mode %q", a.ChecksumMode)
	}

	switch a.Credentials {
	case CredStatic:
		if a.AccessKeyID == "" {
			return a, errors.New("access key ID is required for static credentials")
		}
		a.Profile = ""
	case CredProfile:
		if a.Profile == "" {
			return a, errors.New("a profile name is required")
		}
		a.AccessKeyID = ""
	case CredDefault:
		a.AccessKeyID, a.Profile = "", ""
	case CredAnonymous:
		if a.RoleARN != "" {
			return a, errors.New("anonymous access cannot assume a role")
		}
		a.AccessKeyID, a.Profile = "", ""
	default:
		return a, fmt.Errorf("unknown credentials type %q", a.Credentials)
	}
	if a.RoleARN != "" && !strings.HasPrefix(a.RoleARN, "arn:") {
		return a, errors.New("role ARN must start with \"arn:\"")
	}
	if a.RoleARN == "" && (a.ExternalID != "" || a.RoleSessionName != "") {
		return a, errors.New("external ID and role session name require a role ARN")
	}
	if err := normalizeCDN(&a); err != nil {
		return a, err
	}
	return a, nil
}

func validateSecrets(sec *Secrets) error {
	if sec.SecretAccessKey == "" {
		return errors.New("secret access key is required")
	}
	return nil
}

// SaveAccount creates (empty ID) or updates an account.
//
// sec == nil keeps the stored secrets; otherwise sec replaces them (an empty
// SessionToken removes a stored token). Secrets are only valid together with
// CredStatic. Ordering, so that a failure never loses working credentials:
//
//  1. write the new secrets under a fresh generation of keyring keys;
//  2. persist accounts.json;
//  3. delete the old keyring entries.
//
// Failure in 1 or 2 deletes what step 1 wrote and leaves the old state
// untouched. If only step 3 fails the account IS saved: the returned Account is
// populated and err describes the leftover entry (Kind ErrOther).
func (s *Service) SaveAccount(a Account, sec *Secrets) (Account, error) {
	const op = "SaveAccount"
	a, err := normalizeAccount(a)
	if err != nil {
		return Account{}, invalid(op, "%v", err)
	}
	if sec != nil {
		if a.Credentials != CredStatic {
			return Account{}, invalid(op, "secrets can only be stored for static credentials")
		}
		if err := validateSecrets(sec); err != nil {
			return Account{}, invalid(op, "%v", err)
		}
	}

	s.acctMu.Lock()
	defer s.acctMu.Unlock()
	if sec != nil {
		lgAddSecret(sec.SecretAccessKey)
		lgAddSecret(sec.SessionToken)
	}

	s.mu.Lock()
	var old accountRecord
	exists := false
	if a.ID != "" {
		old, exists = s.accountLocked(a.ID)
	}
	s.mu.Unlock()
	if a.ID == "" {
		a.ID = newID()
	}

	rec := accountRecord{Account: a}
	if exists {
		rec.SecretGen, rec.HasSecret, rec.HasSession = old.SecretGen, old.HasSecret, old.HasSession
	}
	switch {
	case a.Credentials != CredStatic:
		rec.HasSecret, rec.HasSession = false, false
	case sec == nil && !rec.HasSecret:
		return Account{}, invalid(op, "a secret access key is required for static credentials")
	}

	var written []string // keys created by step 1, for rollback
	rollback := func() {
		for _, k := range written {
			s.kr.Delete(k)
		}
	}
	if sec != nil {
		if exists {
			rec.SecretGen = old.SecretGen + 1
		}
		rec.HasSecret, rec.HasSession = true, sec.SessionToken != ""
		if err := s.kr.Set(accountSecretKey(a.ID, rec.SecretGen), sec.SecretAccessKey); err != nil {
			return Account{}, classify(op, fmt.Errorf("store secret in keyring: %w", err))
		}
		written = append(written, accountSecretKey(a.ID, rec.SecretGen))
		if sec.SessionToken != "" {
			if err := s.kr.Set(accountSessionKey(a.ID, rec.SecretGen), sec.SessionToken); err != nil {
				rollback()
				return Account{}, classify(op, fmt.Errorf("store session token in keyring: %w", err))
			}
			written = append(written, accountSessionKey(a.ID, rec.SecretGen))
		}
	}

	s.mu.Lock()
	next := make([]accountRecord, len(s.accounts), len(s.accounts)+1)
	copy(next, s.accounts)
	s.mu.Unlock()
	replaced := false
	for i := range next {
		if next[i].ID == a.ID {
			next[i] = rec
			replaced = true
		}
	}
	if !replaced {
		next = append(next, rec)
	}
	if err := writeJSONAtomic(s.path("accounts.json"), next); err != nil {
		rollback()
		return Account{}, classify(op, err)
	}

	s.mu.Lock()
	s.accounts = next
	s.invalidateLocked(a.ID)
	s.mu.Unlock()
	s.lgInfo("account saved", append(lgAccountAttrs(rec.Account), "created", !exists)...)

	if exists {
		if err := s.deleteSecretEntries(old, rec); err != nil {
			return rec.Account, opErr(op, ErrOther, "account saved, but the previous secret could not be removed from the keyring: %v", err)
		}
	}
	return rec.Account, nil
}

// deleteSecretEntries removes the entries of old that rec no longer uses.
func (s *Service) deleteSecretEntries(old, rec accountRecord) error {
	var errs []error
	if old.HasSecret && !(rec.HasSecret && rec.SecretGen == old.SecretGen) {
		errs = append(errs, s.kr.Delete(accountSecretKey(old.ID, old.SecretGen)))
	}
	if old.HasSession && !(rec.HasSession && rec.SecretGen == old.SecretGen) {
		errs = append(errs, s.kr.Delete(accountSessionKey(old.ID, old.SecretGen)))
	}
	return errors.Join(errs...)
}

// DeleteAccount removes the account and its keyring entries. The account
// record is removed first; if the keyring cleanup then fails the account is
// gone and the error reports the leftover entry.
func (s *Service) DeleteAccount(id string) error {
	const op = "DeleteAccount"
	s.acctMu.Lock()
	defer s.acctMu.Unlock()

	s.mu.Lock()
	old, ok := s.accountLocked(id)
	next := make([]accountRecord, 0, len(s.accounts))
	for _, r := range s.accounts {
		if r.ID != id {
			next = append(next, r)
		}
	}
	s.mu.Unlock()
	if !ok {
		return opErr(op, ErrNotFound, "no account %q", id)
	}
	if err := writeJSONAtomic(s.path("accounts.json"), next); err != nil {
		return classify(op, err)
	}
	s.mu.Lock()
	s.accounts = next
	s.invalidateLocked(id)
	prefix := id + "/"
	for k := range s.bucketRegion {
		if strings.HasPrefix(k, prefix) {
			delete(s.bucketRegion, k)
		}
	}
	s.mu.Unlock()
	s.lgInfo("account deleted", lgAccountAttrs(old.Account)...)

	if err := s.deleteSecretEntries(old, accountRecord{}); err != nil {
		return opErr(op, ErrOther, "account deleted, but its keyring entries could not be removed: %v", err)
	}
	return nil
}

// TestAccount builds a throwaway client from a (and sec, or the stored secrets
// of an existing a.ID when sec is nil) and lists buckets. Nothing is cached or
// saved. Note that anonymous accounts cannot list buckets, so they fail here
// with AccessDenied by design of S3.
func (s *Service) TestAccount(ctx context.Context, a Account, sec *Secrets) error {
	const op = "TestAccount"
	a, err := normalizeAccount(a)
	if err != nil {
		return invalid(op, "%v", err)
	}
	var secrets Secrets
	if a.Credentials == CredStatic {
		if sec != nil {
			if err := validateSecrets(sec); err != nil {
				return invalid(op, "%v", err)
			}
			secrets = *sec
		} else {
			rec, ok := s.account(a.ID)
			if !ok || !rec.HasSecret {
				return invalid(op, "a secret access key is required for static credentials")
			}
			if secrets, err = s.readSecrets(ctx, rec); err != nil {
				return classify(op, err)
			}
		}
	}
	c, err := storage.New(ctx, storageConfig(a, secrets))
	if err != nil {
		return classify(op, err)
	}
	// A connection test should answer quickly: one attempt, bounded time. The
	// SDK's retries would otherwise keep a refused or wrong endpoint spinning
	// for several seconds before the dialog can show the error.
	ctx, cancel := context.WithTimeout(ctx, testAccountTimeout)
	defer cancel()
	_, err = c.S3().ListBuckets(ctx, &s3.ListBucketsInput{}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	return classify(op, err)
}

// testAccountTimeout bounds TestAccount's single ListBuckets attempt.
const testAccountTimeout = 15 * time.Second
