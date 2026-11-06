//go:build pail_demo

package main

import (
	"flag"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/demo"
)

// demoFlag registers --demo: run against an in-memory demo S3 server. Only
// builds made with -tags pail_demo have it.
func demoFlag() *bool {
	return flag.Bool("demo", false, "run against an in-memory demo S3 server (nothing leaves this machine)")
}

// startDemo opens the demo environment; stop closes the service and the
// server and removes the temporary directory.
func startDemo() (svc *core.Service, stop func(), err error) {
	env, err := demo.New("", true)
	if err != nil {
		return nil, nil, err
	}
	return env.Svc, env.Close, nil
}
