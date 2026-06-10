package api

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"

	"fluxio-backend/internal/storage"
)

type envelope struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// metricsSnapshot is the periodic dashboard push payload.
type metricsSnapshot struct {
	Overview        storage.Overview         `json:"overview"`
	TopTalkers      []storage.Talker         `json:"top_talkers"`
	TopApps         []storage.AppCount       `json:"top_apps"`
	ThroughputPoint *storage.ThroughputPoint `json:"throughput_point,omitempty"`
}

func marshalEnvelope(typ string, data any) []byte {
	b, err := json.Marshal(envelope{Type: typ, Data: data})
	if err != nil {
		log.Printf("api: marshal %s envelope: %v", typ, err)
		return nil
	}
	return b
}

func alertEnvelope(a storage.AlertRow) []byte  { return marshalEnvelope("alert", a) }
func metricsEnvelope(m metricsSnapshot) []byte { return marshalEnvelope("metrics", m) }

// BroadcastAlert pushes a live alert to all WebSocket clients. Wire this as the
// alert-bridge callback from the Suricata correlator.
func BroadcastAlert(h *Hub, a storage.AlertRow) {
	if msg := alertEnvelope(a); msg != nil {
		h.Broadcast(msg)
	}
}

// RunMetricsBroadcaster pushes a metrics snapshot every 5s until ctx is done.
// It queries a rolling short window (5m) so the dashboard's "live" view updates.
func RunMetricsBroadcaster(ctx context.Context, h *Hub, r storage.Reader) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			since := time.Now().Add(-5 * time.Minute)
			snap := metricsSnapshot{}
			snap.Overview, _ = r.Overview(ctx, since, "")
			snap.TopTalkers, _ = r.TopTalkers(ctx, since, "", 10)
			snap.TopApps, _ = r.TopApps(ctx, since, "", 10)
			if pts, _ := r.Throughput(ctx, since, "", 1); len(pts) > 0 {
				snap.ThroughputPoint = &pts[len(pts)-1]
			}
			if msg := metricsEnvelope(snap); msg != nil {
				h.Broadcast(msg)
			}
		}
	}
}

// streamHandler upgrades to WebSocket (token already validated at the gate),
// then relays hub messages to the socket until it closes.
func streamHandler(h *Hub) fiber.Handler {
	return websocket.New(func(conn *websocket.Conn) {
		client := h.Register(16)
		defer h.Unregister(client)
		for msg := range client.send {
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	})
}
