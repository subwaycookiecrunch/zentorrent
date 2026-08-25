package main

import (
	"os"
	"sync"
)

// Centralized exit path: Bubble Tea handlers call ExitApp instead of
// os.Exit so background services (DHT crawler, catalog handles) get a
// chance to close cleanly before the process dies.

var (
	shutdownMu  sync.Mutex
	shutdownFns []func()
)

// OnShutdown registers a cleanup function run once at exit.
func OnShutdown(fn func()) {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	shutdownFns = append(shutdownFns, fn)
}

// runShutdown executes every registered cleanup exactly once, last-in
// first-out (closers registered later typically depend on earlier ones).
func runShutdown() {
	shutdownMu.Lock()
	fns := shutdownFns
	shutdownFns = nil
	shutdownMu.Unlock()

	for i := len(fns) - 1; i >= 0; i-- {
		func() {
			defer func() { recover() }()
			fns[i]()
		}()
	}
}

// ExitApp is the app-wide replacement for os.Exit.
func ExitApp(code int) {
	runShutdown()
	os.Exit(code)
}
