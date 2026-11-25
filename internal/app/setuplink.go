package app

import (
	"errors"
	"net/url"
	"strings"
)

// Setup links share the settings of an account as a URL, for example
// pail://account?v=1&name=Data&provider=s3&endpoint=https%3A%2F%2Fs3.example.com.
// Opening one fills in the add-account form; the person who opened it reviews
// the settings and saves the account like any other. A link never adds an
// account by itself.
//
// The format is published: later versions read the parameters below and
// ignore the ones they do not know. Values that do not parse are left out, so
// the form shows its default for them.

// SetupLinkScheme is the URL scheme of setup links.
const SetupLinkScheme = "pail"

// setupLinkHost is the host part of a setup link: pail://account?...
const setupLinkHost = "account"

// setupLinkVersion is the v= parameter that links made by this version carry.
const setupLinkVersion = "1"

// maxSetupLink bounds what ParseSetupLink reads, about 8 KB.
const maxSetupLink = 8 << 10

// ErrNotSetupLink is returned for a URL that is not a Pail setup link.
var ErrNotSetupLink = errors.New("not a Pail setup link")

// SetupLink holds the settings of an account that a setup link carries.
// Strings are trimmed; empty ones are absent from the link. The proxy, TLS
// verification and bucket aliases are never part of a link: they belong to
// the computer the account is used on.
type SetupLink struct {
	Name        string
	Provider    string // ProviderAWS or ProviderS3
	Endpoint    string
	Region      string
	PathStyle   bool
	Credentials string // Cred*; "" when the link names none
	Profile     string
	// AccessKeyID and SecretAccessKey are in a link only when its author typed
	// them for the recipient (SetupLinkFrom never copies an account's own).
	AccessKeyID     string
	SecretAccessKey string
	RoleARN         string
	ExternalID      string
	RoleSessionName string
	ChecksumMode    string
	RequesterPays   bool
	CDNURL          string
	CDNBuckets      []string
	Buckets         []string // "bucket" or "bucket/folder/"
}

// SetupLinkFrom is the link of an account's settings, without its keys.
func SetupLinkFrom(ac Account) SetupLink {
	l := SetupLink{
		Name: ac.Name, Provider: ac.Provider, Region: ac.Region,
		Credentials: ac.Credentials, RoleARN: ac.RoleARN, ExternalID: ac.ExternalID,
		RoleSessionName: ac.RoleSessionName, ChecksumMode: ac.ChecksumMode,
		RequesterPays: ac.RequesterPays, CDNURL: ac.CDNURL,
		CDNBuckets: append([]string(nil), ac.CDNBuckets...),
	}
	if l.Provider != ProviderS3 {
		l.Provider = ProviderAWS
	}
	if l.Provider == ProviderS3 {
		l.Endpoint, l.PathStyle = ac.Endpoint, ac.PathStyle
	}
	if l.Credentials == CredProfile {
		l.Profile = ac.Profile
	}
	for _, b := range ac.Buckets {
		l.Buckets = append(l.Buckets, b.String())
	}
	return l
}

// HasKeys reports whether the link carries an access key or a secret.
func (l SetupLink) HasKeys() bool { return l.AccessKeyID != "" || l.SecretAccessKey != "" }

// URL is the link as text: pail://account?v=1&...
func (l SetupLink) URL() string {
	q := url.Values{}
	q.Set("v", setupLinkVersion)
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			q.Set(k, v)
		}
	}
	set("name", l.Name)
	provider := ProviderAWS
	if l.Provider == ProviderS3 {
		provider = ProviderS3
	}
	q.Set("provider", provider)
	if provider == ProviderS3 {
		set("endpoint", l.Endpoint)
		if !l.PathStyle { // path-style is what a new S3-compatible account starts with
			q.Set("path", "0")
		}
	}
	set("region", l.Region)
	set("creds", l.Credentials)
	if l.Credentials == CredProfile {
		set("profile", l.Profile)
	}
	if l.Credentials == "" || l.Credentials == CredStatic {
		set("ak", l.AccessKeyID)
		if l.SecretAccessKey != "" {
			q.Set("sk", l.SecretAccessKey) // secrets keep their spaces
		}
	}
	set("role", l.RoleARN)
	set("externalId", l.ExternalID)
	set("sessionName", l.RoleSessionName)
	if l.ChecksumMode != "auto" {
		set("checksum", l.ChecksumMode)
	}
	if l.RequesterPays {
		q.Set("requesterPays", "1")
	}
	set("cdn", l.CDNURL)
	set("cdnBuckets", joinTrimmed(l.CDNBuckets))
	set("buckets", joinTrimmed(l.Buckets))
	return SetupLinkScheme + "://" + setupLinkHost + "?" + q.Encode()
}

func joinTrimmed(list []string) string {
	var out []string
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, ",")
}

// ParseSetupLink reads a setup link. It fails only when raw is not a setup
// link at all; a value it cannot use is left out, so the form falls back to
// its default for it. Surrounding spaces, quotes and angle brackets, which
// chat apps and mail add, are ignored.
func ParseSetupLink(raw string) (SetupLink, error) {
	raw = strings.Trim(strings.TrimSpace(raw), "\"'<>")
	if raw == "" || len(raw) > maxSetupLink {
		return SetupLink{}, ErrNotSetupLink
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, SetupLinkScheme) {
		return SetupLink{}, ErrNotSetupLink
	}
	// pail://account?..., and the forms pail:account?... and
	// pail:///account?... that some launchers make of it.
	where := u.Host
	if where == "" {
		where = strings.Trim(u.Opaque+u.Path, "/")
	}
	if !strings.EqualFold(where, setupLinkHost) {
		return SetupLink{}, ErrNotSetupLink
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		// Keep what parses: ParseQuery returns the pairs before the bad one.
		if len(q) == 0 {
			return SetupLink{}, ErrNotSetupLink
		}
	}
	get := func(k string) string { return strings.TrimSpace(q.Get(k)) }
	on := func(k string) bool {
		switch strings.ToLower(get(k)) {
		case "1", "true", "yes", "on":
			return true
		}
		return false
	}
	l := SetupLink{
		Name: get("name"), Region: get("region"), Endpoint: get("endpoint"),
		RoleARN: get("role"), ExternalID: get("externalId"), RoleSessionName: get("sessionName"),
		RequesterPays: on("requesterPays"), CDNURL: get("cdn"),
		CDNBuckets: splitComma(get("cdnBuckets")), Buckets: splitComma(get("buckets")),
		PathStyle: true,
	}
	switch strings.ToLower(get("provider")) {
	case ProviderS3:
		l.Provider = ProviderS3
	case ProviderAWS:
		l.Provider = ProviderAWS
	default: // an endpoint of its own means an S3-compatible service
		l.Provider = ProviderAWS
		if l.Endpoint != "" {
			l.Provider = ProviderS3
		}
	}
	if l.Provider == ProviderS3 {
		if p := strings.ToLower(get("path")); p == "0" || p == "false" || p == "no" || p == "off" {
			l.PathStyle = false
		}
	} else {
		l.Endpoint, l.PathStyle = "", false
	}
	switch c := strings.ToLower(get("creds")); c {
	case CredStatic, CredProfile, CredDefault, CredAnonymous:
		l.Credentials = c
	}
	if l.Credentials == CredProfile {
		l.Profile = get("profile")
	}
	if l.Credentials == "" || l.Credentials == CredStatic {
		l.AccessKeyID = get("ak")
		l.SecretAccessKey = q.Get("sk")
	}
	switch c := strings.ToLower(get("checksum")); c {
	case "auto", "when_required", "when_supported":
		l.ChecksumMode = c
	}
	return l, nil
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
