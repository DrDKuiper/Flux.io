package collector

import (
	"net"
	"testing"
	"time"

	"fluxio-backend/internal/collector/netflowv9"
)

func TestToFlowRecord(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	flow := netflowv9.Flow{
		SrcIP:    net.ParseIP("10.0.0.1").To4(),
		DstIP:    net.ParseIP("93.184.216.34").To4(),
		SrcPort:  51000,
		DstPort:  443,
		Protocol: 6,
		Bytes:    1500,
		Packets:  10,
	}

	rec := toFlowRecord(flow, now)

	if rec.SourceIP != "10.0.0.1" || rec.DestinationIP != "93.184.216.34" {
		t.Errorf("unexpected IPs: %+v", rec)
	}
	if rec.SourcePort != 51000 || rec.DestinationPort != 443 {
		t.Errorf("unexpected ports: %+v", rec)
	}
	if rec.Protocol != 6 || rec.Bytes != 1500 || rec.Packets != 10 {
		t.Errorf("unexpected proto/bytes/packets: %+v", rec)
	}
	if !rec.Timestamp.Equal(now) {
		t.Errorf("expected timestamp %v, got %v", now, rec.Timestamp)
	}
}
