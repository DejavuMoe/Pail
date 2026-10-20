package main

import (
	"strconv"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// secretsState is the "Manage secrets" dialog: the references that upload
// rules use for SSE-C keys and ZIP passwords. Values go to the keyring and are
// never shown again.
type secretsState struct {
	open        bool
	name, value string
	err         string
	busy        bool
}

func (a *app) openSecrets() {
	a.tl.secrets = secretsState{open: true}
}

func (a *app) secretAdd() {
	s := &a.tl.secrets
	name := strings.TrimSpace(s.name)
	switch {
	case s.busy:
		return
	case name == "":
		s.err = locales.SecretsEnterName()
		return
	case s.value == "":
		s.err = locales.SecretsEnterValue()
		return
	}
	value := s.value
	s.busy, s.err = true, ""
	a.bg(func() func() {
		err := a.svc.SetSecret(name, value)
		return func() {
			s.busy = false
			if err != nil {
				s.err = a.noteErr(err, "secret", func() {
					if s.open && s.name == name && s.value == value {
						a.secretAdd()
					}
				})
				return
			}
			s.name, s.value = "", ""
			a.toast(locales.SecretsSaved(name))
		}
	})
}

func (a *app) secretAskDelete(name string) {
	a.confirm = confirmState{
		open: true, title: locales.SecretsDeleteTitle(),
		rows:    []KV{{locales.SecretsRowSecret(), name}},
		message: locales.SecretsDeleteMessage(),
		action:  locales.CommonDelete(), danger: true,
		run: func() {
			s := &a.tl.secrets
			a.bg(func() func() {
				err := a.svc.DeleteSecret(name)
				return func() {
					if err != nil {
						s.err = humanError(err)
						return
					}
					a.toast(locales.SecretsDeleted(name))
				}
			})
		},
	}
}

func (a *app) secretsDialog(c *ui.Context) {
	k := tk(c)
	s := &a.tl.secrets
	if !s.open {
		return
	}
	refs := a.svc.SecretRefs()
	dialog(c, &s.open, locales.SecretsTitle(), func() {
		ui.Column(c).Gap(2).Padding(8, 10).Radius(6).Background(k.Sidebar).MaxHeight(168).Children(func() {
			if len(refs) == 0 {
				ui.Text(c, locales.SecretsNone()).FontSize(sizeSecondary).TextColor(k.TextMuted)
				return
			}
			ui.Scroll(c).MaxHeight(152).ScrollbarInsets(0, 0, 0, 0).Children(func() {
				ui.Column(c).Padding(0, scrollGutter, 0, 0).Children(func() {
					for i, r := range refs {
						ui.Row(c.Key("sref:" + strconv.Itoa(i))).Height(26).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, icons.Get("key-round")).FontSize(14).TextColor(k.TextMuted).Shrink(0)
							mono(ui.Text(c, r).FontSize(sizeSecondary).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0))
							if iconButton(c, icons.Get("trash-2"), locales.SecretsDeleteLabel(r)).Clicked() {
								a.secretAskDelete(r)
							}
						})
					}
				})
			})
		})
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
					in := inputBox(c, &s.name, locales.SecretsNameLabel(), locales.CommonName(), inputOpts{mono: true, invalid: s.err != "" && strings.TrimSpace(s.name) == ""})
					if in.Changed() {
						s.err = ""
					}
				})
				ui.Box(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
					in := inputBox(c, &s.value, locales.SecretsValueLabel(), locales.CommonValue(), inputOpts{password: true})
					if in.Changed() {
						s.err = ""
					}
					if in.Submitted() {
						a.secretAdd()
					}
				})
				if secondaryButton(c, locales.CommonAdd(), s.busy).Clicked() {
					a.secretAdd()
				}
			})
			if s.err != "" {
				ui.Text(c, s.err).FontSize(sizeSecondary).TextColor(k.Danger)
			}
		})
	}, func() {
		buttonRow(c, func() {
			if primaryButton(c, locales.CommonDone(), false).Clicked() {
				s.open = false
			}
		})
	})
}
