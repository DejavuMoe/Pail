package main

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
)

// The terminal on the landing page shows what the pail command really prints.
// site/cli-demo.json is written by TestSiteCLIDemo in cmd/pail, which runs the
// commands against the demo S3 server and fails when their output changes. The
// first example is rendered into the page, so it works without JavaScript; all
// of them are embedded as JSON for app.js, which only picks the colours.

type cliLine struct {
	Kind string `json:"kind"` // cmd, out (stdout) or err (stderr)
	Text string `json:"text"`
}

type cliExample struct {
	ID      string    `json:"id"`
	Command string    `json:"command"`
	Lines   []cliLine `json:"lines"`
}

// Tab is the label of the example's tab.
func (e cliExample) Tab() string {
	if e.ID == "json" {
		return "--json"
	}
	return e.ID
}

type cliDemo struct {
	Examples []cliExample `json:"examples"`
}

var cliIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func loadCLIDemo(root string) (cliDemo, error) {
	var d cliDemo
	p := filepath.Join(root, "site", "cli-demo.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return d, err
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return d, fmt.Errorf("%s: %w", p, err)
	}
	if len(d.Examples) == 0 {
		return d, fmt.Errorf("%s: no examples", p)
	}
	seen := map[string]bool{}
	for _, e := range d.Examples {
		if !cliIDRE.MatchString(e.ID) || seen[e.ID] {
			return d, fmt.Errorf("%s: example id %q is invalid or repeated", p, e.ID)
		}
		seen[e.ID] = true
		if len(e.Lines) == 0 || e.Lines[0].Kind != "cmd" || e.Lines[0].Text != e.Command {
			return d, fmt.Errorf("%s: example %q must start with its command", p, e.ID)
		}
	}
	return d, nil
}

var (
	reTermUpload = regexp.MustCompile(`^(upload|download)\s`)
	reTermDelete = regexp.MustCompile(`^delete\s`)
	reTermDryRun = regexp.MustCompile(`^dry run:`)
)

// termClass is the CSS class of a line. app.js (termKind) makes the same choice
// for the examples it renders; TestTerminalClassesMatchScript keeps them equal.
func termClass(l cliLine) string {
	switch {
	case l.Kind == "cmd":
		return "t-cmd"
	case l.Kind == "err":
		return "t-dim"
	case reTermUpload.MatchString(l.Text):
		return "t-ok"
	case reTermDelete.MatchString(l.Text):
		return "t-del"
	case reTermDryRun.MatchString(l.Text):
		return "t-dim"
	}
	return "t-out"
}

// termLine renders one terminal line as the page and app.js both write it.
func termLine(l cliLine) template.HTML {
	text := html.EscapeString(l.Text)
	if l.Kind == "cmd" {
		return template.HTML(`<span class="t-prompt">$</span> <span class="t-cmd">` + text + `</span>`)
	}
	return template.HTML(`<span class="` + termClass(l) + `">` + text + `</span>`)
}
