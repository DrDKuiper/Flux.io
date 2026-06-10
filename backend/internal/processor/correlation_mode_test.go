package processor

import (
	"testing"
	"time"
)

func TestGetForModeFiltersByMechanism(t *testing.T) {
	c := NewCorrelationCache(time.Minute)
	tuple := FiveTuple{SrcIP: "a", DstIP: "b", SrcPort: 1, DstPort: 2, Protocol: 6}
	c.Put(tuple, DPIMetadata{Application: "tls"}, "suricata")

	if _, ok := c.GetForMode(tuple, "none"); ok {
		t.Error("mode none must never return metadata")
	}
	if _, ok := c.GetForMode(tuple, "tzsp"); ok {
		t.Error("mode tzsp must not return a suricata-tagged entry")
	}
	if m, ok := c.GetForMode(tuple, "suricata"); !ok || m.Application != "tls" {
		t.Errorf("mode suricata should return the entry, got ok=%v m=%+v", ok, m)
	}
	if m, ok := c.GetForMode(tuple, "auto"); !ok || m.Application != "tls" {
		t.Errorf("mode auto should return any entry, got ok=%v m=%+v", ok, m)
	}
}
