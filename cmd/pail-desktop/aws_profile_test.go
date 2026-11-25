package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

func TestAccountDialogAWSProfilePickerAndRegion(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.a.localAWS = func() (core.AWSLocal, error) {
		return core.AWSLocal{DefaultProfile: "default", Profiles: []core.AWSProfile{
			{Name: "default", Region: "us-east-1", Auth: core.ProfileAuthStatic},
			{Name: "work", Region: "ap-northeast-1", Auth: core.ProfileAuthLogin},
		}}, nil
	}
	h.a.openAccountForm(false)
	h.settle()
	if h.a.af.region != "us-east-1" {
		t.Fatalf("new static account region = %q, want us-east-1", h.a.af.region)
	}

	// Switching to AWS profile clears the untouched default region so the
	// profile's own region takes effect.
	h.mustClick("Access keys")
	h.mustClick("AWS profile")
	if h.a.af.region != "" {
		t.Fatalf("switching to AWS profile kept default region %q", h.a.af.region)
	}
	h.mustHave("Profile", "Choose…")

	// Pick the discovered aws login profile.
	h.mustClick("Choose…")
	h.mustHave("work", "aws login · ap-northeast-1", "Other…")
	h.mustClick("work")
	if h.a.af.profile != "work" {
		t.Fatalf("profile = %q, want work", h.a.af.profile)
	}
	if r := h.a.af.selectedProfileRegion(); r != "ap-northeast-1" {
		t.Fatalf("selectedProfileRegion = %q, want ap-northeast-1", r)
	}

	// Save the AWS profile account and verify the sidebar shows the profile name
	// when no explicit region was saved.
	h.a.af.name = "Work AWS"
	h.mustClick("Save")
	h.waitFor("account saved", func() bool { return len(h.env.Svc.Accounts()) == 2 && !h.a.af.open })
	got := h.env.Svc.Accounts()[1]
	if got.Provider != core.ProviderAWS || got.Credentials != core.CredProfile || got.Profile != "work" || got.Region != "" {
		t.Fatalf("saved account = %+v", got)
	}
	h.mustHave("Work AWS", "work")
}

func TestAccountDialogAWSProfileCustomAndEdit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.a.localAWS = func() (core.AWSLocal, error) {
		return core.AWSLocal{DefaultProfile: "default", Profiles: []core.AWSProfile{
			{Name: "work", Region: "ap-northeast-1", Auth: core.ProfileAuthLogin},
		}}, nil
	}

	// Choosing "Other…" reveals the manual profile name input.
	h.a.openAccountForm(false)
	h.settle()
	h.mustClick("Access keys")
	h.mustClick("AWS profile")
	h.mustClick("Choose…")
	h.mustClick("Other…")
	if h.a.af.profilePick != customProfile {
		t.Fatalf("profilePick = %q, want %q", h.a.af.profilePick, customProfile)
	}
	h.a.af.name = "Custom AWS"
	h.a.af.profile = "custom-prof"
	h.mustClick("Save")
	h.waitFor("saved", func() bool { return len(h.env.Svc.Accounts()) == 2 && !h.a.af.open })

	// Editing an account whose profile is not in the discovered list selects
	// Other… and keeps the saved profile name.
	h.a.openAccountForm(true)
	h.settle()
	if h.a.af.profilePick != customProfile || h.a.af.profile != "custom-prof" {
		t.Fatalf("edit custom profile: pick=%q profile=%q", h.a.af.profilePick, h.a.af.profile)
	}
}

// The region placeholder shows the region the SDK would use: the default
// chain reads AWS_PROFILE's profile, and AWS_REGION beats every profile.
func TestAccountDialogRegionPlaceholderFollowsEnvironment(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	local := core.AWSLocal{DefaultProfile: "work", Profiles: []core.AWSProfile{
		{Name: "default", Region: "us-east-1", Auth: core.ProfileAuthStatic},
		{Name: "work", Region: "ap-northeast-1", Auth: core.ProfileAuthLogin},
	}}
	h.a.localAWS = func() (core.AWSLocal, error) { return local, nil }
	h.a.openAccountForm(false)
	h.settle()
	h.mustClick("Access keys")
	h.mustClick("Default chain")
	if h.a.af.region != "" {
		t.Fatalf("switching to the default chain kept default region %q", h.a.af.region)
	}
	if r := h.a.af.selectedProfileRegion(); r != "ap-northeast-1" {
		t.Fatalf("default chain with AWS_PROFILE=work: region = %q, want ap-northeast-1", r)
	}

	local.EnvRegion = "eu-central-1"
	h.a.openAccountForm(false)
	h.settle()
	h.mustClick("Access keys")
	h.mustClick("Default chain")
	if r := h.a.af.selectedProfileRegion(); r != "eu-central-1" {
		t.Fatalf("default chain with AWS_REGION: region = %q, want eu-central-1", r)
	}
	h.mustClick("Default chain")
	h.mustClick("AWS profile")
	h.mustClick("Choose…")
	h.mustClick("default")
	if r := h.a.af.selectedProfileRegion(); r != "eu-central-1" {
		t.Fatalf("profile with AWS_REGION: region = %q, want eu-central-1", r)
	}
}

func TestHumanErrorLoginRequired(t *testing.T) {
	err := &core.OpError{
		Op:      "Buckets",
		Kind:    core.ErrAccessDenied,
		Message: "AWS login session is missing or expired; run aws login to sign in again",
	}
	wrapped := fmt.Errorf("wrap: %w", errors.Join(core.ErrLoginRequired, err))
	if got := humanError(wrapped); got != locales.TypesLoginRequired() || !strings.Contains(got, "aws login") {
		t.Fatalf("humanError(ErrLoginRequired) = %q", got)
	}
}
