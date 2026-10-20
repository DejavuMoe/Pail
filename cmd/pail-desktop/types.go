package main

import (
	"errors"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

// The UI speaks the application layer's vocabulary directly.
type (
	Scope   = core.Location
	Entry   = core.Entry
	Bucket  = core.Bucket
	Account = core.Account
	Task    = core.Task
)

// KV is one label/value row of the inspector.
type KV struct{ Key, Value string }

// humanError words an application error for the screen. AccessDenied,
// NotFound and Network get a short plain sentence; everything else keeps the
// service's own message.
func humanError(err error) string {
	if err == nil {
		return ""
	}
	var oe *core.OpError
	if !errors.As(err, &oe) {
		return err.Error()
	}
	code := ""
	if oe.Code != "" {
		code = " (" + oe.Code + ")"
	}
	switch oe.Kind {
	case core.ErrAccessDenied:
		return locales.TypesAccessDenied() + code
	case core.ErrNotFound:
		return locales.TypesNotFound() + code
	case core.ErrNetwork:
		return locales.TypesNetwork()
	case core.ErrCanceled:
		return locales.TypesCanceled()
	case core.ErrUnsupported:
		return locales.TypesUnsupported() + code
	case core.ErrLocked:
		return locales.TypesLocked()
	}
	if msg := strings.TrimSpace(oe.Message); msg != "" {
		return msg
	}
	return err.Error()
}
