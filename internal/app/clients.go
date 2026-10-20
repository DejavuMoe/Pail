package app

import (
	"context"
	"errors"
	"sync"

	"github.com/DejavuMoe/Pail/internal/cloud"
	"github.com/DejavuMoe/Pail/internal/storage"
)

// clientEntry caches what is needed to talk to one account: the storage.Config
// (which contains the secrets read from the keyring once) and lazily built
// storage and CloudFront clients. An entry is immutable after creation apart
// from the lazily built clients; saving or deleting the account drops it.
type clientEntry struct {
	cfg storage.Config
	acc Account

	mu sync.Mutex // guards st and cl
	st *storage.Client
	cl *cloud.Client
}

// buildCall lets concurrent callers share one (possibly slow, keyring-prompting)
// configuration build instead of each triggering their own prompt.
type buildCall struct{ done chan struct{} }

// storageConfig translates an Account and its secrets to the protocol layer.
func storageConfig(a Account, sec Secrets) storage.Config {
	cfg := storage.Config{
		Endpoint:           a.Endpoint,
		Region:             a.Region,
		UsePathStyle:       a.PathStyle,
		RoleARN:            a.RoleARN,
		ExternalID:         a.ExternalID,
		RoleSessionName:    a.RoleSessionName,
		InsecureSkipVerify: a.InsecureSkipVerify,
		ChecksumMode:       a.ChecksumMode,
	}
	switch a.Credentials {
	case CredStatic:
		cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken = a.AccessKeyID, sec.SecretAccessKey, sec.SessionToken
	case CredProfile:
		cfg.UseDefaultCredentials, cfg.Profile = true, a.Profile
	case CredDefault:
		cfg.UseDefaultCredentials = true
	case CredAnonymous:
		cfg.Anonymous = true
	}
	return cfg
}

// readSecrets loads the stored secrets of rec from the keyring. It must be
// called without s.mu held; it returns early when ctx ends.
func (s *Service) readSecrets(ctx context.Context, rec accountRecord) (Secrets, error) {
	var sec Secrets
	if !rec.HasSecret {
		return sec, nil
	}
	v, err := keyringGet(ctx, s.kr, accountSecretKey(rec.ID, rec.SecretGen))
	if errors.Is(err, ErrSecretNotFound) {
		return sec, opErr("", ErrNotFound, "the secret access key of account %q is missing from the keyring; edit the account to enter it again", rec.Name)
	}
	if err != nil {
		return sec, err
	}
	sec.SecretAccessKey = v
	if rec.HasSession {
		v, err := keyringGet(ctx, s.kr, accountSessionKey(rec.ID, rec.SecretGen))
		if err != nil && !errors.Is(err, ErrSecretNotFound) {
			return sec, err
		}
		sec.SessionToken = v
	}
	return sec, nil
}

// invalidateLocked drops the cached entry of id and bumps its epoch so that a
// build already in flight is discarded. Caller holds s.mu.
func (s *Service) invalidateLocked(id string) {
	s.acctEpoch[id]++
	delete(s.clients, id)
	delete(s.condUnsupported, id) // the endpoint may have changed
}

// entry returns the cached configuration of account id, building it (keyring
// read outside s.mu) on first use.
func (s *Service) entry(ctx context.Context, id string) (*clientEntry, error) {
	if s.vaultLast.Load() != 0 {
		s.vaultTouch() // using an account counts as vault activity (idle auto-lock)
	}
	for {
		s.mu.Lock()
		if e := s.clients[id]; e != nil {
			s.mu.Unlock()
			return e, nil
		}
		rec, ok := s.accountLocked(id)
		if !ok {
			s.mu.Unlock()
			return nil, opErr("", ErrNotFound, "no account %q", id)
		}
		if c := s.building[id]; c != nil {
			s.mu.Unlock()
			select {
			case <-c.done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		call := &buildCall{done: make(chan struct{})}
		s.building[id] = call
		epoch := s.acctEpoch[id]
		s.mu.Unlock()

		sec, err := s.readSecrets(ctx, rec) // may block on an OS dialog; no lock held
		var e *clientEntry
		if err == nil {
			e = &clientEntry{acc: rec.Account, cfg: storageConfig(rec.Account, sec)}
		}

		s.mu.Lock()
		if s.building[id] == call {
			delete(s.building, id)
		}
		fresh := s.acctEpoch[id] == epoch
		if e != nil && fresh {
			s.clients[id] = e
		}
		s.mu.Unlock()
		close(call.done)

		switch {
		case !fresh:
			// The account was saved or deleted while we were reading (the
			// old keyring entry may even be gone already); start over.
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			continue
		case err != nil:
			return nil, err
		}
		return e, nil
	}
}

// storageClient returns the (cached) S3 client of an account.
func (s *Service) storageClient(ctx context.Context, id string) (*storage.Client, error) {
	e, err := s.entry(ctx, id)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.st == nil {
		c, err := storage.New(ctx, e.cfg)
		if err != nil {
			return nil, err
		}
		e.st = c
	}
	return e.st, nil
}

// cloudClient returns the (cached) CloudFront client of an account.
func (s *Service) cloudClient(ctx context.Context, id string) (*cloud.Client, error) {
	e, err := s.entry(ctx, id)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cl == nil {
		c, err := cloud.New(ctx, e.cfg)
		if err != nil {
			return nil, err
		}
		e.cl = c
	}
	return e.cl, nil
}
