// Command mb8611-poller is the entry point for the MB8611 status poller.
//
// This file is an intentionally minimal placeholder created by task 2.1 to
// bootstrap the module. Its only job is to keep the go-monitor and yaml.v3
// requires non-unused so `go mod tidy` does not strip them before later
// phases wire in the real implementation. Task 2.2 replaces this with the
// real stub wiring.
package main

import (
	// Blank imports keep these modules as required dependencies. yaml.v3 is
	// not consumed until Phase 6, so a blank import is used here so that
	// `go mod tidy` retains the require without forcing premature usage.
	_ "github.com/danweinerdev/go-monitor"
	_ "gopkg.in/yaml.v3"
)

func main() {}
