//go:build !pail_demo

package main

import (
	"errors"

	core "github.com/DejavuMoe/Pail/internal/app"
)

// Release builds carry no demo: no --demo flag, no in-memory S3 server and no
// sample data in the binary. Developers build it in with -tags pail_demo
// (demo_on.go).

// demoFlag registers nothing and reports false.
func demoFlag() *bool { return new(bool) }

// startDemo is never reached without the flag.
func startDemo() (*core.Service, func(), error) {
	return nil, nil, errors.New("this build has no demo; build with -tags pail_demo")
}
