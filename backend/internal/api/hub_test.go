package api

import (
	"testing"
	"time"
)

func TestHubBroadcastReachesClients(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	c := h.Register(4)
	defer h.Unregister(c)

	h.Broadcast([]byte("hello"))

	select {
	case msg := <-c.send:
		if string(msg) != "hello" {
			t.Fatalf("expected hello, got %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not receive the broadcast")
	}
}

func TestHubDropsSlowClientWithoutBlocking(t *testing.T) {
	h := NewHub()
	go h.Run()
	defer h.Stop()

	c := h.Register(1)
	defer h.Unregister(c)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.Broadcast([]byte("x"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked on a slow client")
	}
}
