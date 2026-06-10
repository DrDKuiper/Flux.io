package processor

import "testing"

func TestFiveTupleAsMapKey(t *testing.T) {
	cache := map[FiveTuple]string{}
	key := FiveTuple{SrcIP: "10.0.0.1", DstIP: "93.184.216.34", SrcPort: 51000, DstPort: 443, Protocol: 6}
	cache[key] = "example.com"

	same := FiveTuple{SrcIP: "10.0.0.1", DstIP: "93.184.216.34", SrcPort: 51000, DstPort: 443, Protocol: 6}
	got, ok := cache[same]
	if !ok || got != "example.com" {
		t.Fatalf("expected lookup by equal FiveTuple to hit cache, got ok=%v val=%q", ok, got)
	}
}
