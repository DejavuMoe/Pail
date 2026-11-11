package main

import (
	"fmt"
	"testing"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

// A save that gave up waiting for another Pail process shows the localized
// sentence, not the English sentinel text.
func TestHumanErrorConfigBusy(t *testing.T) {
	err := fmt.Errorf("save account: %w", core.ErrConfigLocked)
	if got, want := humanError(err), locales.TypesConfigBusy(); got != want {
		t.Fatalf("humanError = %q, want %q", got, want)
	}
}
