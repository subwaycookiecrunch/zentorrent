package search

// TEMPORARY verification probe for a code-review finding. Delete after run.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// guardedRaceTrial reproduces verbatim the guard at dht.go:178-182 (and the
// identical one at dht.go:301-305) executed by two goroutines at once.
// Returns whether either goroutine panicked.
func guardedRaceTrial() bool {
	done := make(chan struct{})
	results := make(chan bool, 2)
	var wg sync.WaitGroup
	var start chan struct{}
	start = make(chan struct{})
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			panicked := false
			func() {
				defer func() {
					if r := recover(); r != nil {
						panicked = true
					}
				}()
				<-start
				select { // verbatim dht.go:178-182
				case <-done:
				default:
					close(done)
				}
			}()
			results <- panicked
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	a, b := <-results, <-results
	return a || b
}

func TestZZGuardedCloseAtomicity(t *testing.T) {
	const trials = 20000
	fired := 0
	for i := 0; i < trials; i++ {
		if guardedRaceTrial() {
			fired++
		}
	}
	t.Logf("verbatim guard pattern: %d/%d trials panicked (%.2f%%)",
		fired, trials, 100*float64(fired)/trials)
}

// TestZZRealAPIRunVsClose drives the actual DHTIndexer through the documented
// shutdown: cancel ctx (waking Run's teardown at :300-305) while Close()
// (:177-188) runs concurrently. An unrecovered double-close crashes the test
// binary with `panic: close of closed channel`.
func TestZZRealAPIRunVsClose(t *testing.T) {
	const iters = 500
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "probe.db")
	for i := 0; i < iters; i++ {
		idx, err := NewDHTIndexer(DHTConfig{DBPath: dbPath})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		runReturned := make(chan struct{})
		go func() {
			defer close(runReturned)
			_ = idx.Run(ctx) // no recover(): a double-close must crash loudly
		}()
		cancel()
		go func() { _ = idx.Close() }()
		<-runReturned
		if i%100 == 99 {
			fmt.Printf("  real-api probe: %d/%d iterations clean\n", i+1, iters)
		}
	}
}
