package client

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

func newCheckerWithFetcher(t *testing.T) (*ResolverChecker, *Fetcher) {
	t.Helper()
	// Use localhost non-listening ports so no real DNS traffic leaves the machine.
	f := newTestFetcher(t, []string{"127.0.0.1:19753", "127.0.0.1:19754"})
	rc := NewResolverChecker(f, 200*time.Millisecond)
	return rc, f
}

func TestResolverChecker_DefaultTimeout(t *testing.T) {
	f := newTestFetcher(t, []string{"127.0.0.1:19753"})
	rc := NewResolverChecker(f, 0)
	if rc.timeout != 15*time.Second {
		t.Errorf("default timeout = %v, want 15s", rc.timeout)
	}
}

func TestResolverChecker_CheckNow_SkipsWhenRunning(t *testing.T) {
	rc, _ := newCheckerWithFetcher(t)

	// Manually lock to simulate a running scan.
	rc.scanRunMu.Lock()

	// CheckNow should return false immediately (TryLock fails).
	result := rc.CheckNow(context.Background())
	if result {
		t.Error("CheckNow should return false when another scan is running")
	}

	rc.scanRunMu.Unlock()
}

func TestResolverChecker_CheckNow_CancelledContext(t *testing.T) {
	rc, _ := newCheckerWithFetcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	result := rc.CheckNow(ctx)
	if result {
		t.Error("CheckNow should return false with cancelled context")
	}
}

func TestResolverChecker_CheckNow_NoResolvers(t *testing.T) {
	f := newTestFetcher(t, nil) // no resolvers
	rc := NewResolverChecker(f, 200*time.Millisecond)

	result := rc.CheckNow(context.Background())
	if !result {
		t.Error("CheckNow should return true when there are no resolvers")
	}
}

// udpBlackhole opens a UDP listener that reads and discards all packets.
// Returns the address and a cleanup function.
func udpBlackhole(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			_, _, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			// discard — never respond
		}
	}()
	return conn.LocalAddr().String()
}

func TestResolverChecker_CancelCurrentScan(t *testing.T) {
	// UDP blackhole: listener accepts packets but never responds,
	// so probes block until their context is cancelled.
	addr := udpBlackhole(t)
	f := newTestFetcher(t, []string{addr})
	rc := NewResolverChecker(f, 30*time.Second)

	var scanDone sync.WaitGroup
	scanDone.Add(1)
	go func() {
		defer scanDone.Done()
		rc.CheckNow(context.Background())
	}()

	// Give the goroutine time to start.
	time.Sleep(50 * time.Millisecond)

	// Cancel should make the running scan return.
	rc.CancelCurrentScan()
	scanDone.Wait() // should not hang
}

func TestResolverChecker_ConcurrentCheckNow_OnlyOneRuns(t *testing.T) {
	rc, _ := newCheckerWithFetcher(t)

	// Fire 5 concurrent CheckNow calls.
	var wg sync.WaitGroup
	results := make([]bool, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = rc.CheckNow(context.Background())
		}(i)
	}
	wg.Wait()

	// At most 1 should have actually run (returned true or false depending on
	// the mock — but only 1 should have entered the scan body).
	trueCount := 0
	for _, r := range results {
		if r {
			trueCount++
		}
	}
	// Most should be false (skipped), at most 1 true.
	if trueCount > 1 {
		t.Errorf("expected at most 1 CheckNow to run, got %d", trueCount)
	}
}

func TestResolverChecker_StartAndNotify_OnlyOnce(t *testing.T) {
	rc, _ := newCheckerWithFetcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// StartAndNotify should only allow one start — the second call is a no-op.
	rc.StartAndNotify(ctx, nil)

	// started flag should be true.
	if !rc.started.Load() {
		t.Error("started flag should be true after StartAndNotify")
	}

	// Second call should not panic and should be a no-op.
	rc.StartAndNotify(ctx, nil)
	cancel()
}

func TestResolverChecker_SetLogFunc(t *testing.T) {
	rc, _ := newCheckerWithFetcher(t)

	var logged []string
	var mu sync.Mutex
	rc.SetLogFunc(func(msg string) {
		mu.Lock()
		logged = append(logged, msg)
		mu.Unlock()
	})

	rc.log("test %d", 42)

	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || logged[0] != "test 42" {
		t.Errorf("logged = %v, want [test 42]", logged)
	}
}
