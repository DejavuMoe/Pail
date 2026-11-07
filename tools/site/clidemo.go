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
	// A line with aligned columns keeps its spaces and scrolls sideways; any
	// other line (a command, JSON, a URL) wraps at the edge of the terminal.
	reTermTable = regexp.MustCompile(`\S {2,}\S`)
)

// termClass is the CSS classes of a line. app.js (termKind) makes the same choice
// for the examples it renders; TestTerminalClassesMatchScript keeps them equal.
func termClass(l cliLine) string {
	c := "t-out"
	switch {
	case l.Kind == "cmd":
		c = "t-cmd"
	case l.Kind == "err":
		c = "t-dim"
	case reTermUpload.MatchString(l.Text):
		c = "t-ok"
	case reTermDelete.MatchString(l.Text):
		c = "t-del"
	case reTermDryRun.MatchString(l.Text):
		c = "t-dim"
	}
	if !reTermTable.MatchString(l.Text) {
		c += " t-wrap"
	}
	return c
}

// termLine renders one terminal line as the page and app.js both write it.
func termLine(l cliLine) template.HTML {
	text := html.EscapeString(l.Text)
	if l.Kind == "cmd" {
		return template.HTML(`<span class="t-prompt">$</span> <span class="` + termClass(l) + `">` + text + `</span>`)
	}
	return template.HTML(`<span class="` + termClass(l) + `">` + text + `</span>`)
}
