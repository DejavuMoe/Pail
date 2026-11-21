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
// notices of what ships inside the app. The header is two lines beside the
// icon: the name and the version, which opens its release notes (and from
// there the repository), then the author and license links.
func (a *app) aboutDialog(c *ui.Context) {
	k := tk(c)
	dialog(c, &a.about.open, locales.AboutTitle(), func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			if b := appIconBitmap(); b != nil {
				ui.Image(c, b).Size(56, 56).Radius(RadiusAbout).Shrink(0)
			} else {
				ui.Icon(c, icons.Get("database")).FontSize(32).TextColor(k.TextMuted)
			}
			ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
				// The version link's text starts 8px inside its hover fill, the
				// gap between it and the name.
				ui.Row(c).AlignItems(ui.Baseline).Children(func() {
					ui.Text(c, "Pail").FontSize(sizeTitle).FontWeight(600).TextColor(k.Text).SingleLine()
					a.versionLink(c, locales.AboutVersion(version), true)
				})
				// A link's text starts 8px inside its hover fill: -8 puts the
				// first icon on the name's left edge.
				ui.Row(c).Margin(0, 0, 0, -8).AlignItems(ui.Center).Children(func() {
					a.authorLink(c, locales.AboutAuthorLink(authorName))
					a.licenseLink(c)
				})
			})
		})
		hairline(c)
		ui.Column(c).Gap(4).Children(func() {
			sectionLabel(c, strings.ToUpper(locales.AboutThirdParty()))
			notice := func(name, license, url string) {
				ui.Row(c.Key("notice:" + url)).Height(28).Gap(12).AlignItems(ui.Center).Children(func() {
					a.link(c, "github", name, locales.AboutOpenNotice(name), url).Margin(0, 0, 0, -8).Shrink(1).MinWidth(0)
					ui.Spacer(c)
					ui.Text(c, license).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Shrink(0)
				})
			}
			ui.Column(c).Children(func() {
				notice(locales.AboutLucide(), locales.AboutLicenseIsc(), lucideURL)
				notice(locales.AboutInter(), locales.AboutLicenseOfl(), interURL)
			})
		})
	}, func() {
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonClose(), false).Clicked() {
				a.about.open = false
			}
		})
	})
}

// Where the About links go. The third-party projects link to their
// repositories, where their licenses are.
const (
	licenseURL = repoURL + "/blob/master/LICENSE"
	lucideURL  = "https://github.com/lucide-icons/lucide"
	interURL   = "https://github.com/rsms/inter"
)

// releaseURL is the release page of the running version; a build without a
// version stamp ("dev") has none and gets the list of releases.
func releaseURL() string {
	if version == "" || version == "dev" {
		return repoURL + "/releases"
	}
	return repoURL + "/releases/tag/v" + strings.TrimPrefix(version, "v")
}

// link is a text button that opens url in the browser. The Lucide icon says
// what is behind it (a website, a repository, a release, a license) rather
// than repeating that it opens externally; the tooltip says where it goes.
func (a *app) link(c *ui.Context, icon, text, tip, url string) ui.Element {
	return linkButton(c, icons.Get(icon), text, tip, false).OnClick(func() { a.openExternal(url) })
}

// versionLink is the button that opens the release notes of this version;
// quiet draws it as secondary text, beside the name in the About dialog.
func (a *app) versionLink(c *ui.Context, text string, quiet bool) ui.Element {
	return linkButton(c, icons.Get("tag"), text, locales.AboutOpenRelease(), quiet).OnClick(func() { a.openExternal(releaseURL()) })
}

// authorLink is the button that opens the author's website; text is what it shows.
func (a *app) authorLink(c *ui.Context, text string) ui.Element {
	return a.link(c, "globe", text, locales.AboutOpenAuthor(), authorURL).Label(authorName)
}

// repoLink is the button that opens the repository.
func (a *app) repoLink(c *ui.Context) ui.Element {
	return a.link(c, "github", strings.TrimPrefix(repoURL, "https://"), locales.AboutOpenProject(), repoURL).
		Label(locales.AboutProjectPage())
}

// licenseLink is the button that opens the license text.
func (a *app) licenseLink(c *ui.Context) ui.Element {
	return a.link(c, "scale", locales.AboutLicense(), locales.AboutOpenLicense(), licenseURL)
}

// aboutSection is the "About" block at the end of Settings: version, author,
// repository and license, each a link: the release of this version, the
// author's website, the repository and the license text.
func (a *app) aboutSection(c *ui.Context) {
	// A ghost button's text starts and ends 8px inside its hover fill: the
	// negative margin puts it on the edge the other rows' values end on.
	a.settingsGroup(c, "about", func() {
		a.settingsCard(c, func() {
			a.settingsRow(c, locales.AboutVersionLabel(), "", func() {
				a.versionLink(c, version, false).Margin(0, -8, 0, 0)
			})
			a.settingsRow(c, locales.AboutAuthor(), "", func() {
				a.authorLink(c, authorName).Margin(0, -8, 0, 0)
			})
			a.settingsRow(c, locales.AboutRepository(), "", func() {
				a.repoLink(c).Margin(0, -8, 0, 0)
			})
			a.settingsRow(c, locales.AboutLicenseLabel(), "", func() {
				a.licenseLink(c).Margin(0, -8, 0, 0)
			})
		})
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
