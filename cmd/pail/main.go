// Command pail is the command-line front end of Pail. It shares the
// configuration directory and OS keyring with the desktop app and is built on
// internal/app only. See docs/cli.md.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"

	"github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/selfupdate"
)

func main() {
	// What an update could not remove while the old version ran.
	selfupdate.CleanLeftovers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
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
	go onInterrupt(sigs, cancel, func() { signal.Stop(sigs) }, os.Stderr, io.StderrTTY)
	code := run(ctx, os.Args[1:], io)
	signal.Stop(sigs)
	cancel()
	os.Exit(code)
}

// onInterrupt handles the first interrupt: it cancels the command, restores
// the default signal handling (so that a second Ctrl-C ends the process at
// once) and says so on w. Later signals are not read.
func onInterrupt(sigs <-chan os.Signal, cancel context.CancelFunc, restore func(), w io.Writer, tty bool) {
	if _, ok := <-sigs; !ok {
		return
	}
	restore()
	if tty {
		fmt.Fprint(w, "\r\x1b[K") // the live progress line
	}
	fmt.Fprintln(w, "pail: interrupted; canceling and cleaning up. Press Ctrl-C again to exit at once without cleanup.")
	cancel()
}

func isTerminal(f *os.File) bool {
	// ModeCharDevice is not enough: NUL and /dev/null are character devices too.
	return term.IsTerminal(int(f.Fd()))
}

// readPassword asks on the terminal without echoing the input.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

var _ app.Keyring // IO.Keyring is an app.Keyring; nil selects the OS keyring.
