package storage

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// AWSProfile.Auth values, matching the AWS SDK credential resolution order.
const (
	ProfileAuthLogin       = "login"
	ProfileAuthSSO         = "sso"
	ProfileAuthStatic      = "static"
	ProfileAuthRole        = "role"
	ProfileAuthProcess     = "process"
	ProfileAuthWebIdentity = "web_identity"
)

// AWSProfile describes one profile found in the local AWS shared configuration
// and credentials files. It never carries secret values.
type AWSProfile struct {
	Name   string
	Region string
	Auth   string
}

func sharedConfigFilename() string {
	if p := strings.TrimSpace(os.Getenv("AWS_CONFIG_FILE")); p != "" {
		return p
	}
	return awsconfig.DefaultSharedConfigFilename()
}

func sharedCredentialsFilename() string {
	if p := strings.TrimSpace(os.Getenv("AWS_SHARED_CREDENTIALS_FILE")); p != "" {
		return p
	}
	return awsconfig.DefaultSharedCredentialsFilename()
}

// AWSEnvRegion returns the region set in the environment (AWS_REGION, else
// AWS_DEFAULT_REGION). The SDK prefers it over every profile's region.
func AWSEnvRegion() string { return firstEnv("AWS_REGION", "AWS_DEFAULT_REGION") }

// AWSEnvProfile returns the profile the default credential chain reads:
// AWS_PROFILE, else AWS_DEFAULT_PROFILE, else "default".
func AWSEnvProfile() string {
	if p := firstEnv("AWS_PROFILE", "AWS_DEFAULT_PROFILE"); p != "" {
		return p
	}
	return "default"
}

// firstEnv mirrors the SDK: the first variable with a non-empty value wins.
func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// SharedConfigStamp returns a lightweight stat fingerprint of the AWS shared
// configuration and credentials files. Callers use it to notice when commands
// such as `aws login --profile` or `aws configure` update those files on disk.
func SharedConfigStamp() string {
	return fileStamp(sharedConfigFilename()) + ";" + fileStamp(sharedCredentialsFilename())
}

func fileStamp(path string) string {
	if path == "" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return path + "|missing"
	}
	return fmt.Sprintf("%s|%d|%d", path, fi.Size(), fi.ModTime().UnixNano())
}

type rawProfile struct {
	region          string
	hasAccessKey    bool
	hasSecretKey    bool
	hasSource       bool
	hasCredSource   bool
	hasWebIdentity  bool
	hasSSO          bool
	hasLoginSession bool
	hasProcess      bool
	hasRoleARN      bool
}

func (r *rawProfile) auth() string {
	switch {
	case r.hasSource:
		return ProfileAuthRole
	case r.hasAccessKey && r.hasSecretKey:
		return ProfileAuthStatic
	case r.hasCredSource:
		return ProfileAuthRole
	case r.hasWebIdentity:
		return ProfileAuthWebIdentity
	case r.hasSSO:
		return ProfileAuthSSO
	case r.hasLoginSession:
		return ProfileAuthLogin
	case r.hasProcess:
		return ProfileAuthProcess
	case r.hasRoleARN:
		return ProfileAuthRole
	default:
		return ""
	}
}

// ListAWSProfiles returns the profiles defined in the local AWS shared config
// and credentials files (respecting AWS_CONFIG_FILE and
// AWS_SHARED_CREDENTIALS_FILE). Missing files are ignored; "default" comes
// first when present, followed by the remaining profiles sorted by name.
func ListAWSProfiles() ([]AWSProfile, error) {
	profiles := map[string]*rawProfile{}
	if err := readAWSINI(sharedConfigFilename(), true, profiles); err != nil {
		return nil, err
	}
	if err := readAWSINI(sharedCredentialsFilename(), false, profiles); err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == "default" {
			return true
		}
		if names[j] == "default" {
			return false
		}
		return names[i] < names[j]
	})
	out := make([]AWSProfile, 0, len(names))
	for _, name := range names {
		rp := profiles[name]
		out = append(out, AWSProfile{
			Name:   name,
			Region: rp.region,
			Auth:   rp.auth(),
		})
	}
	return out, nil
}

func readAWSINI(path string, isConfig bool, out map[string]*rawProfile) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	var cur *rawProfile
	var hasAccessKey, hasSecretKey bool
	commitStatic := func() {
		// AWS requires both keys in the same file to treat static keys as complete.
		if cur != nil && hasAccessKey && hasSecretKey {
			cur.hasAccessKey, cur.hasSecretKey = true, true
		}
		hasAccessKey, hasSecretKey = false, false
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			line = strings.TrimPrefix(line, "\uFEFF")
			first = false
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
			continue
		}
		if trimmed[0] == '[' {
			commitStatic()
			cur = nil
			end := strings.IndexByte(trimmed, ']')
			if end < 0 {
				continue
			}
			sec := strings.TrimSpace(trimmed[1:end])
			name, ok := parseProfileSection(sec, isConfig)
			if !ok {
				continue
			}
			cur = out[name]
			if cur == nil {
				cur = &rawProfile{}
				out[name] = cur
			}
			continue
		}
		if cur == nil {
			continue
		}
		// Indented lines belong to nested blocks (such as `s3 =`), not top-level profile keys.
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trimmed[:eq]))
		val := strings.TrimSpace(trimmed[eq+1:])
		if val == "" {
			continue
		}
		switch key {
		case "region":
			cur.region = val
		case "aws_access_key_id":
			hasAccessKey = true
		case "aws_secret_access_key":
			hasSecretKey = true
		case "source_profile":
			cur.hasSource = true
		case "credential_source":
			cur.hasCredSource = true
		case "web_identity_token_file":
			cur.hasWebIdentity = true
		case "sso_session", "sso_start_url":
			cur.hasSSO = true
		case "login_session":
			cur.hasLoginSession = true
		case "credential_process":
			cur.hasProcess = true
		case "role_arn":
			cur.hasRoleARN = true
		}
	}
	commitStatic()
	return sc.Err()
}

func parseProfileSection(sec string, isConfig bool) (string, bool) {
	if sec == "" {
		return "", false
	}
	if isConfig {
		if sec == "default" {
			return "default", true
		}
		if strings.HasPrefix(sec, "profile ") || strings.HasPrefix(sec, "profile\t") {
			name := strings.TrimSpace(sec[len("profile"):])
			return name, name != ""
		}
		return "", false
	}
	// Shared credentials files do not use the "profile " prefix.
	if strings.HasPrefix(sec, "profile ") || strings.HasPrefix(sec, "profile\t") {
		return "", false
	}
	return sec, true
}
