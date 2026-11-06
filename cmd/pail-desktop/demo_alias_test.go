package main

import "github.com/DejavuMoe/Pail/internal/demo"

// The demo environment lives in internal/demo, so release builds do not carry
// it; the tests use it under these names.
type demoEnv = demo.Env

const (
	demoAccountName = demo.AccountName
	demoVersioned   = demo.Versioned
)

var (
	newDemoEnv  = demo.New
	demoBuckets = demo.Buckets
)
