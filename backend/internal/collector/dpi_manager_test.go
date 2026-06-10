package collector

import (
	"context"
	"sync"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestReconcileStartsAndStopsMechanisms(t *testing.T) {
	var mu sync.Mutex
	running := map[string]bool{}
	mk := func(name string) sourceRunFunc {
		return func(ctx context.Context) {
			mu.Lock()
			running[name] = true
			mu.Unlock()
			<-ctx.Done()
			mu.Lock()
			running[name] = false
			mu.Unlock()
		}
	}
	m := NewDPIManager(nil, DPIManagerSources{Suricata: mk("suricata"), TZSP: mk("tzsp")})
	ctx := context.Background()

	m.Reconcile(ctx, true, false) // want suricata only
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return running["suricata"] && !running["tzsp"] })

	m.Reconcile(ctx, true, true) // add tzsp
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return running["suricata"] && running["tzsp"] })

	m.Reconcile(ctx, false, false) // stop all
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return !running["suricata"] && !running["tzsp"] })
}

func TestReconcileIsIdempotent(t *testing.T) {
	var mu sync.Mutex
	starts := map[string]int{}
	mk := func(name string) sourceRunFunc {
		return func(ctx context.Context) {
			mu.Lock()
			starts[name]++
			mu.Unlock()
			<-ctx.Done()
		}
	}
	m := NewDPIManager(nil, DPIManagerSources{Suricata: mk("suricata"), TZSP: mk("tzsp")})
	ctx := context.Background()

	m.Reconcile(ctx, true, false)
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return starts["suricata"] == 1 })
	m.Reconcile(ctx, true, false) // same set — must not restart
	m.Reconcile(ctx, true, false)
	time.Sleep(30 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if starts["suricata"] != 1 {
		t.Fatalf("expected suricata to start exactly once, got %d", starts["suricata"])
	}
	m.Stop()
}
