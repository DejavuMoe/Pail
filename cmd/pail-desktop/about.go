package main

import (
	"strings"
	"sync"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

type aboutState struct{ open bool }

type keysState struct{ open bool }

var (
	iconOnce   sync.Once
	iconBitmap *ui.Bitmap
)

// appIconBitmap decodes the embedded application icon once.
func appIconBitmap() *ui.Bitmap {
	iconOnce.Do(func() {
		if b, err := ui.DecodeBitmap(appIcon); err == nil {
			iconBitmap = b
		}
	})
	return iconBitmap
}

// aboutDialog shows the icon, the version, the license and the third-party
// notices of what ships inside the app.
func (a *app) aboutDialog(c *ui.Context) {
	k := tk(c)
	dialog(c, &a.about.open, locales.AboutTitle(), func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			if b := appIconBitmap(); b != nil {
				ui.Image(c, b).Size(56, 56).Radius(12).Shrink(0)
			} else {
				ui.Icon(c, icons.Get("database")).FontSize(32).TextColor(k.TextMuted)
			}
			ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, "Pail").FontSize(18).FontWeight(600).TextColor(k.Text)
				mutedText(c, locales.AboutVersion(version))
				mutedText(c, locales.AboutLicense())
				a.authorLink(c, locales.AboutAuthorLink(authorName)).Margin(0, 0, 0, -8).AlignSelf(ui.Start)
			})
		})
		hairline(c)
		ui.Column(c).Gap(8).Children(func() {
			sectionLabel(c, strings.ToUpper(locales.AboutThirdParty()))
			notice := func(name, license string) {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, name).FontSize(sizeBody).TextColor(k.Text).Grow(1).MinWidth(0)
					ui.Text(c, license).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Shrink(0)
				})
			}
			notice(locales.AboutLucide(), locales.AboutLicenseIsc())
			notice(locales.AboutInter(), locales.AboutLicenseOfl())
		})
		ghostButton(c, icons.Get("external-link"), strings.TrimPrefix(repoURL, "https://"), locales.AboutOpenProject(), false).
			Label(locales.AboutProjectPage()).Margin(0, 0, 0, -8).AlignSelf(ui.Start).OnClick(func() { a.openExternal(repoURL) })
	}, func() {
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonClose(), false).Clicked() {
				a.about.open = false
			}
		})
	})
}

// authorLink is the button that opens the author's website; text is what it shows.
func (a *app) authorLink(c *ui.Context, text string) ui.Element {
	return ghostButton(c, icons.Get("external-link"), text, locales.AboutOpenAuthor(), false).
		Label(authorName).OnClick(func() { a.openExternal(authorURL) })
}

// aboutSection is the "About" block at the end of Settings: version, author,
// repository and license, with the links of the About dialog.
func (a *app) aboutSection(c *ui.Context) {
	settingsSection(c, locales.SettingsAbout())
	settingsRow(c, locales.AboutVersionLabel(), "", func() {
		ui.Text(c, version).FontSize(sizeBody).TextColor(tk(c).Text).SingleLine()
	})
	settingsRow(c, locales.AboutAuthor(), "", func() {
		a.authorLink(c, authorName).Margin(0, 0, 0, -8)
	})
	settingsRow(c, locales.AboutRepository(), "", func() {
		ghostButton(c, icons.Get("external-link"), strings.TrimPrefix(repoURL, "https://"), locales.AboutOpenProject(), false).
			Label(locales.AboutProjectPage()).Margin(0, 0, 0, -8).OnClick(func() { a.openExternal(repoURL) })
	})
	settingsRow(c, locales.AboutLicenseLabel(), "", func() {
		ui.Text(c, locales.AboutLicense()).FontSize(sizeBody).TextColor(tk(c).Text).SingleLine()
	})
}

// keysDialog lists the shortcuts of the command table by heading.
func (a *app) keysDialog(c *ui.Context) {
	k := tk(c)
	dialogW(c, &a.keys.open, locales.KeysTitle(), 480, func() {
		for _, g := range paletteGroups {
			var rows []*command
			for i := range a.commands {
				if cmd := &a.commands[i]; cmd.Group == g && len(cmd.Chords) > 0 {
					rows = append(rows, cmd)
				}
			}
			if len(rows) == 0 {
				continue
			}
			ui.Column(c.Key("keys:" + g)).Gap(6).Children(func() {
				sectionLabel(c, strings.ToUpper(groupName(locales.In(i18n.Locale()), g)))
				hairline(c)
				for _, cmd := range rows {
					var names []string
					for _, ch := range cmd.Chords {
						names = append(names, ch.String())
					}
					ui.Row(c.Key(cmd.ID)).Height(24).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, strings.TrimSuffix(cmd.text(), "…")).FontSize(sizeBody).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0)
						ui.Text(c, strings.Join(names, "  ·  ")).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Shrink(0)
					})
				}
			})
		}
	}, func() {
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonClose(), false).Clicked() {
				a.keys.open = false
			}
		})
	})
}
