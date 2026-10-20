// Command pail is the command-line front end of Pail. It shares the
// configuration directory and OS keyring with the desktop app and is built on
// internal/app only. See docs/cli.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/DejavuMoe/Pail/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	io := IO{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		StdinTTY:  isTerminal(os.Stdin),
		StderrTTY: isTerminal(os.Stderr),
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		io.ReadPassword = readPassword
	}
	code := run(ctx, os.Args[1:], io)
	stop()
	os.Exit(code)
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// readPassword asks on the terminal without echoing the input.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

var _ app.Keyring // IO.Keyring is an app.Keyring; nil selects the OS keyring.
