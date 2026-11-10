package app

import (
	"context"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ProxySettings is the proxy server of the app, in settings.json. Scheme ""
// means no proxy of the app's own: requests follow HTTP_PROXY, HTTPS_PROXY and
// NO_PROXY from the environment. The password is not in the file; it is in
// the secret store, and HasPassword says that it is there.
type ProxySettings struct {
	Scheme      string `json:"scheme,omitempty"` // "" | ProxyHTTP | ProxyHTTPS | ProxySOCKS5
	Host        string `json:"host,omitempty"`   // a host name or an IP address, without brackets
	Port        int    `json:"port,omitempty"`
	Username    string `json:"username,omitempty"`
	HasPassword bool   `json:"hasPassword,omitempty"`
}

// ProxySettings.Scheme values.
const (
	ProxyHTTP   = "http"
	ProxyHTTPS  = "https"
	ProxySOCKS5 = "socks5"
)

// ProxyProblem says what is wrong with a ProxySettings; "" is nothing.
type ProxyProblem string

// ProxyProblem values.
const (
	ProxyOK           ProxyProblem = ""
	ProxyBadScheme    ProxyProblem = "scheme"       // not "", http, https or socks5
	ProxyHostMissing  ProxyProblem = "host-missing" // a scheme without a host
	ProxyHostInvalid  ProxyProblem = "host-invalid" // not a host name or an IP address
	ProxyPortInvalid  ProxyProblem = "port"         // not 1 to 65535
	ProxyUserMissing  ProxyProblem = "user"         // a password without a user name
	ProxyUserInvalid  ProxyProblem = "user-invalid" // control characters, or too long
	maxProxyUserRunes              = 255
	maxProxyPassRunes              = 255
)

// proxyPasswordKey names the proxy password entry in the secret store.
const proxyPasswordKey = "proxy/password"

var proxyHostRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// cleaned trims the text fields and drops everything else when there is no scheme.
func (p ProxySettings) cleaned() ProxySettings {
	p.Scheme = strings.ToLower(strings.TrimSpace(p.Scheme))
	if p.Scheme == "" {
		return ProxySettings{}
	}
	p.Host = strings.TrimSpace(p.Host)
	if len(p.Host) > 2 && p.Host[0] == '[' && p.Host[len(p.Host)-1] == ']' {
		p.Host = p.Host[1 : len(p.Host)-1] // an IPv6 address may be typed with brackets
	}
	p.Username = strings.TrimSpace(p.Username)
	return p
}

// Problem checks p as SaveProxy will. newPassword says that a password is
// being given with it.
func (p ProxySettings) Problem(newPassword bool) ProxyProblem {
	p = p.cleaned()
	switch p.Scheme {
	case "":
		return ProxyOK
	case ProxyHTTP, ProxyHTTPS, ProxySOCKS5:
	default:
		return ProxyBadScheme
	}
	switch {
	case p.Host == "":
		return ProxyHostMissing
	case net.ParseIP(p.Host) == nil && !proxyHostRE.MatchString(p.Host) || len(p.Host) > 253:
		return ProxyHostInvalid
	case p.Port < 1 || p.Port > 65535:
		return ProxyPortInvalid
	case utf8.RuneCountInString(p.Username) > maxProxyUserRunes || strings.ContainsFunc(p.Username, unicode.IsControl):
		return ProxyUserInvalid
	case p.Username == "" && (newPassword || p.HasPassword):
		return ProxyUserMissing
	}
	return ProxyOK
}

// address is the proxy as a URL, "" when there is none. The password is the
// caller's: it never comes from the settings file.
func (p ProxySettings) address(password string) string {
	if p.Scheme == "" {
		return ""
	}
	u := &url.URL{Scheme: p.Scheme, Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}
	switch {
	case p.Username != "" && password != "":
		u.User = url.UserPassword(p.Username, password)
	case p.Username != "":
		u.User = url.User(p.Username)
	}
	return u.String()
}

func proxyProblemText(pr ProxyProblem) string {
	switch pr {
	case ProxyBadScheme:
		return "the proxy protocol must be HTTP, HTTPS or SOCKS5"
	case ProxyHostMissing:
		return "the proxy needs a host name or an IP address"
	case ProxyHostInvalid:
		return "the proxy host must be a host name or an IP address"
	case ProxyPortInvalid:
		return "the proxy port must be between 1 and 65535"
	case ProxyUserMissing:
		return "a proxy password needs a user name"
	case ProxyUserInvalid:
		return "the proxy user name must not contain control characters"
	}
	return "invalid proxy"
}

// proxyAddress reads the proxy password from the secret store (outside every
// lock: the store may ask the user) and returns the address requests go
// through, "" when the app has no proxy of its own.
func (s *Service) proxyAddress(ctx context.Context, p ProxySettings) (string, error) {
	if p.Scheme == "" {
		return "", nil
	}
	pw := ""
	if p.HasPassword {
		v, err := keyringGet(ctx, s.kr, proxyPasswordKey)
		if errors.Is(err, ErrSecretNotFound) {
			return "", opErr("", ErrNotFound, "the proxy password is missing from the secret store; enter it again in Settings")
		}
		if err != nil {
			return "", err
		}
		pw = v
	}
	return p.address(pw), nil
}

// SaveProxy saves the proxy server. password nil keeps the stored password; a
// value replaces it ("" removes it). A proxy with no Scheme removes the user
// name and the password too. A password needs a user name; clearing the user
// name removes the password.
//
// The password goes to the secret store, never to settings.json. The cached
// clients of every account are dropped, so that the next request uses the new
// proxy; transfers already running finish on their old connections. Like the
// other secret writers it fails with a Locked error while the vault is locked.
func (s *Service) SaveProxy(p ProxySettings, password *string) error {
	const op = "SaveProxy"
	p = p.cleaned()
	newPassword := password != nil && *password != ""
	if utf8.RuneCountInString(derefString(password)) > maxProxyPassRunes {
		return invalid(op, "the proxy password may have at most %d characters", maxProxyPassRunes)
	}

	s.refMu.Lock() // the secret writers' lock: a backend switch cannot run meanwhile
	defer s.refMu.Unlock()
	s.mu.Lock()
	cur := s.settings.Proxy
	s.mu.Unlock()

	switch {
	case p.Scheme == "" || p.Username == "":
		if newPassword {
			return invalid(op, "%s", proxyProblemText(ProxyUserMissing))
		}
		p.HasPassword = false
	case password != nil:
		p.HasPassword = newPassword
	default:
		p.HasPassword = cur.HasPassword
	}
	if pr := p.Problem(newPassword); pr != ProxyOK {
		return invalid(op, "%s", proxyProblemText(pr))
	}

	// 1. the new password, 2. settings.json, 3. the old password if it is gone.
	var rollback func()
	if newPassword {
		lgAddSecret(*password)
		old, oldErr := s.kr.Get(proxyPasswordKey)
		if err := s.kr.Set(proxyPasswordKey, *password); err != nil {
			return classify(op, err)
		}
		rollback = func() {
			if oldErr == nil {
				s.kr.Set(proxyPasswordKey, old)
			} else {
				s.kr.Delete(proxyPasswordKey)
			}
		}
	}

	s.wmu.Lock()
	s.mu.Lock()
	st := s.settings
	s.mu.Unlock()
	st.Proxy = p
	if err := writeJSONAtomic(s.path("settings.json"), st); err != nil {
		s.wmu.Unlock()
		if rollback != nil {
			rollback()
		}
		return classify(op, err)
	}
	s.mu.Lock()
	s.settings = st
	if cur != p || newPassword {
		// Clients are built with the proxy URL, which carries the password: drop
		// them (and the builds under way, by their epoch) so that the next
		// request uses the new one. A password written counts as a change even
		// when the settings struct is equal.
		for _, r := range s.accounts {
			s.invalidateLocked(r.ID)
		}
	}
	s.mu.Unlock()
	s.wmu.Unlock()
	if cur != p {
		s.lgInfo("proxy changed", "scheme", p.Scheme, "was", cur.Scheme, "auth", p.HasPassword)
	}

	if cur.HasPassword && !p.HasPassword {
		if err := s.kr.Delete(proxyPasswordKey); err != nil {
			return opErr(op, ErrOther, "the proxy was saved, but the old password could not be removed from the secret store: %v", err)
		}
	}
	return nil
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
