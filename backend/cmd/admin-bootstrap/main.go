package main

import (
	"os"
)

// main is the entry point for the admin-bootstrap command. The actual work
// lives in runBootstrap so tests can drive the binary without touching
// os.Exit or os.Args directly.
func main() {
	err := runBootstrap(os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	if err != nil {
		os.Exit(1)
	}
}
