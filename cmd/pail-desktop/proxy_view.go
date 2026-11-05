package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// The Network section of Settings: the proxy server. Its fields only mean
// something together (a protocol without a host is no proxy), so unlike the
// other settings they are saved by one Apply button, not as each changes.
// Choosing "From environment" and applying removes the proxy and its password.

// proxyForm is what the Network section edits: text fields, so that a
// half-typed port never snaps back, and the state of the last Apply.
type proxyForm struct {
	scheme, host, port, user, pass string
	tried                          bool   // Apply was pressed: the problems show
	busy                           bool   // the save is running
	err                            string // the save failed: why
}

func proxyFormOf(p core.ProxySettings) proxyForm {
	f := proxyForm{scheme: p.Scheme, host: p.Host, user: p.Username}
	if p.Port > 0 {
		f.port = strconv.Itoa(p.Port)
	}
	return f
}

// settings is the proxy the fields describe. A port that is not a number is 0,
// which Problem reports.
func (f *proxyForm) settings() core.ProxySettings {
	port, _ := strconv.Atoi(strings.TrimSpace(f.port))
	return core.ProxySettings{Scheme: f.scheme, Host: f.host, Port: port, Username: f.user}
}

// problem is what keeps the fields from being applied.
func (f *proxyForm) problem() core.ProxyProblem { return f.settings().Problem(f.pass != "") }

// differs reports whether Apply would change something: another proxy than the
// saved one, or a password typed.
func (f *proxyForm) differs(saved core.ProxySettings) bool {
	if f.scheme == "" {
		return saved.Scheme != ""
	}
	d := f.settings()
	return d.Scheme != saved.Scheme || strings.Trim(strings.TrimSpace(d.Host), "[]") != saved.Host ||
		d.Port != saved.Port || strings.TrimSpace(d.Username) != saved.Username || f.pass != ""
}

// proxyChoices are the protocols; "" follows the environment. HTTP, HTTPS and
// SOCKS5 are protocol names and stay as they are in every language.
func proxyChoices() []choice {
	return []choice{
		{"", locales.SettingsProxyEnv()},
		{core.ProxyHTTP, "HTTP"},
		{core.ProxyHTTPS, "HTTPS"},
		{core.ProxySOCKS5, "SOCKS5"},
	}
}

// proxyProblemText says a problem in the language shown now.
func proxyProblemText(p core.ProxyProblem) string {
	switch p {
	case core.ProxyHostMissing:
		return locales.SettingsProxyErrHostMissing()
	case core.ProxyHostInvalid:
		return locales.SettingsProxyErrHost()
	case core.ProxyPortInvalid:
		return locales.SettingsProxyErrPort()
	case core.ProxyUserMissing:
		return locales.SettingsProxyErrUser()
	case core.ProxyUserInvalid:
		return locales.SettingsProxyErrUserChars()
	}
	return ""
}

// applyProxy saves the fields. A problem shows under its field and nothing is
// written; a failed save is explained under the button and the fields stay.
func (a *app) applyProxy() {
	f := &a.sf.proxy
	f.tried, f.err = true, ""
	if f.busy || f.problem() != core.ProxyOK {
		return
	}
	p := f.settings()
	var pw *string
	if f.pass != "" {
		v := f.pass
		pw = &v
	}
	f.busy = true
	a.busyOps.Add(1)
	go func() {
		err := a.svc.SaveProxy(p, pw)
		a.post(func() {
			a.busyOps.Add(-1)
			f := &a.sf.proxy
			f.busy = false
			if err != nil {
				f.err = locales.SettingsErrSave(humanError(err))
				return
			}
			*f = proxyFormOf(a.svc.Settings().Proxy) // the cleaned form that was saved
			if a.sf.saved == nil {
				a.sf.saved = map[string]time.Time{}
			}
			a.sf.saved["proxy"] = time.Now()
		})
	}()
}

// networkSection is the Network section: protocol, host and port, and the
// optional user name and password.
func (a *app) networkSection(c *ui.Context) {
	f := &a.sf.proxy
	k := tk(c)
	saved := a.svc.Settings().Proxy
	settingsSection(c, locales.SettingsNetwork())
	settingsRowHelp(c, locales.SettingsProxy(), locales.SettingsProxyHelp(), "", func() {
		if selectBox(c, locales.SettingsProxyProtocol(), &f.scheme, proxyChoices(), 180) {
			f.tried, f.err = false, ""
		}
		a.savedMark(c, "proxy")
	})
	if f.scheme == "" && !f.differs(saved) {
		return
	}
	if f.scheme != "" {
		pr := core.ProxyOK
		if f.tried {
			pr = f.problem()
		}
		addrErr, userErr := "", ""
		switch pr {
		case core.ProxyHostMissing, core.ProxyHostInvalid, core.ProxyPortInvalid:
			addrErr = proxyProblemText(pr)
		case core.ProxyUserMissing, core.ProxyUserInvalid:
			userErr = proxyProblemText(pr)
		}
		settingsRow(c, locales.SettingsProxyAddress(), addrErr, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
				ui.Box(c).Grow(1).MinWidth(0).Children(func() {
					in := inputBox(c, &f.host, locales.SettingsProxyHost(), locales.SettingsProxyHost(), inputOpts{
						invalid: addrErr != "" && pr != core.ProxyPortInvalid, mono: f.host != ""})
					if in.Submitted() {
						a.applyProxy()
					}
				})
				in := inputBox(c, &f.port, locales.SettingsProxyPort(), locales.SettingsProxyPort(), inputOpts{
					width: 96, invalid: pr == core.ProxyPortInvalid, mono: f.port != ""})
				if in.Submitted() {
					a.applyProxy()
				}
			})
		})
		settingsRow(c, locales.SettingsProxyUser(), userErr, func() {
			in := inputBox(c, &f.user, locales.SettingsProxyUser(), "", inputOpts{width: 240, invalid: pr == core.ProxyUserInvalid})
			if in.Submitted() {
				a.applyProxy()
			}
		})
		hint := ""
		if saved.HasPassword && f.pass == "" && strings.TrimSpace(f.user) == saved.Username {
			hint = locales.SettingsProxyPasswordSaved()
		}
		settingsRow(c, locales.SettingsProxyPassword(), "", func() {
			in := inputBox(c, &f.pass, locales.SettingsProxyPassword(), hint, inputOpts{width: 240, password: true, invalid: pr == core.ProxyUserMissing})
			if in.Submitted() {
				a.applyProxy()
			}
		})
	}
	if !f.differs(saved) {
		return // the fields show the saved proxy: nothing to apply
	}
	ui.Column(c.Key("proxy-apply")).Gap(4).MarginStart(216).Children(func() {
		if primaryButton(c, locales.SettingsProxyApply(), f.busy).AlignSelf(ui.Start).Clicked() {
			a.applyProxy()
		}
		if f.err != "" {
			ui.Text(c, f.err).FontSize(sizeSecondary).TextColor(k.Danger)
		}
	})
}
