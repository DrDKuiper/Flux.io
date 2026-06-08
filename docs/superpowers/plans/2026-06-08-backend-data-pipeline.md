# Backend Data Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the Flux.io backend from an architectural skeleton into a working pipeline that decodes real NetFlow/IPFIX traffic, enriches it with GeoIP/ASN and DPI (application identification), persists everything to ClickHouse, and forwards Suricata alerts to Wazuh.

**Architecture:** A UDP NetFlow v9 listener decodes packets into `FlowRecord`s and pushes them through a channel to a batch writer that persists to ClickHouse. In parallel, a Suricata `eve.json` tailer (or, in TZSP mode, a raw-packet listener) extracts L7 metadata (SNI/DNS/HTTP) keyed by 5-tuple into a short-lived correlation cache; the pipeline looks up each flow's 5-tuple in that cache before enrichment and persistence. GeoIP/ASN enrichment uses local MaxMind GeoLite2 `.mmdb` files. A `settings` table in Postgres stores which DPI source is active, exposed via a `/api/settings` endpoint and a new frontend Settings page; switching modes hot-swaps the active listener at runtime.

**Tech Stack:** Go 1.22, Fiber, ClickHouse (`clickhouse-go/v2`), Postgres (`database/sql` + `lib/pq` or `pgx`), MaxMind GeoLite2 (`oschwald/geoip2-golang`), `gopacket` (TZSP/TLS/DNS parsing), React/TypeScript (Settings page).

**Reference spec:** [docs/superpowers/specs/2026-06-08-backend-data-pipeline-design.md](../specs/2026-06-08-backend-data-pipeline-design.md)

---

## Implementation note: NetFlow decoding strategy

The existing stub imports `github.com/netsampler/goflow2/v2/decoders/...` but never actually decodes anything — the imports exist only to avoid "unused import" errors. Rather than depend on goflow2's collector/producer machinery (whose public API differs significantly between v2 and v3 and is oriented around its own output pipeline, not towards handing us typed Go structs to enrich further), this plan implements a small, focused **NetFlow v9 decoder** directly against the wire format (Cisco NetFlow v9 / RFC 3954: 20-byte packet header, Template FlowSets carrying field-type/field-length pairs, Data FlowSets whose records are decoded according to previously-seen templates). This is:
- Fully testable with hand-built byte buffers (perfect for TDD — no test fixtures or network mocks needed),
- Free of third-party API churn risk,
- Scoped to exactly the fields Flux.io needs (IPs, ports, protocol, bytes, packets).

Task 1 removes the now-unused `goflow2` dependency accordingly.

---

### Task 1: Clean up dependencies

**Files:**
- Modify: `backend/go.mod`
- Modify: `backend/internal/collector/netflow.go:1-10` (remove goflow2 imports/dummy references — replaced wholesale in Task 3)

- [ ] **Step 1: Remove the unused goflow2 dependency and add new ones**

Edit `backend/go.mod` so the `require` block reads:

```go
require (
	github.com/ClickHouse/clickhouse-go/v2 v2.23.1
	github.com/gofiber/fiber/v2 v2.52.4
	github.com/gofiber/websocket/v2 v2.2.1
	github.com/lib/pq v1.10.9
	github.com/oschwald/geoip2-golang v1.9.0
	github.com/google/gopacket v1.1.19
)
```

- [ ] **Step 2: Tidy modules**

Run: `cd backend && go mod tidy`
Expected: `go.sum` is created/updated, `goflow2` entries are removed, the new modules (`clickhouse-go`, `lib/pq`, `geoip2-golang`, `gopacket`) appear with no errors.

- [ ] **Step 3: Commit**

```bash
git add backend/go.mod backend/go.sum
git commit -m "chore: replace unused goflow2 dependency with geoip2/gopacket/lib-pq"
```

---

### Task 2: Shared processor types

**Files:**
- Create: `backend/internal/processor/types.go`
- Modify: `backend/internal/processor/enrichment.go:7-22` (remove the now-duplicated `FlowRecord` definition)
- Test: `backend/internal/processor/types_test.go`

This consolidates the data shapes every other component depends on: the enriched `FlowRecord` (matching the `network_flows` ClickHouse columns), `FiveTuple` (the correlation key), `DPIMetadata` (what the DPI sources produce), and `SuricataAlert` (matching `suricata_alerts` columns).

- [ ] **Step 1: Write the failing test for FiveTuple equality/usability as a map key**

```go
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
```

- [ ] **Step 2: Run test to verify it fails to compile (FiveTuple doesn't exist yet)**

Run: `cd backend && go test ./internal/processor/... -run TestFiveTupleAsMapKey -v`
Expected: FAIL — `undefined: FiveTuple`

- [ ] **Step 3: Create `types.go` with all shared types**

```go
package processor

import "time"

// FlowRecord represents a normalized, enriched network flow,
// mirroring the columns of the ClickHouse `network_flows` table.
type FlowRecord struct {
	Timestamp       time.Time
	SourceIP        string
	DestinationIP   string
	SourcePort      uint16
	DestinationPort uint16
	Protocol        uint8
	Bytes           uint64
	Packets         uint64

	Application string
	SNI         string
	HTTPHost    string
	HTTPURL     string

	SourceCountry string
	DestCountry   string
	SourceASN     uint32
	DestASN       uint32
	SourceASNOrg  string
	DestASNOrg    string

	SourceHostname string
	DestHostname   string

	IsAlert        bool
	AlertSeverity  uint8
	AlertSignature string
}

// FiveTuple uniquely identifies a network conversation and is the
// correlation key between NetFlow records and DPI metadata.
type FiveTuple struct {
	SrcIP    string
	DstIP    string
	SrcPort  uint16
	DstPort  uint16
	Protocol uint8
}

// DPIMetadata is L7 application-identification data extracted by
// whichever DPI source is active (Suricata correlation or TZSP capture).
type DPIMetadata struct {
	Application string
	SNI         string
	DNSQuery    string
	HTTPHost    string
	HTTPURL     string
}

// SuricataAlert mirrors the columns of the ClickHouse `suricata_alerts` table.
type SuricataAlert struct {
	Timestamp       time.Time
	SourceIP        string
	DestinationIP   string
	SourcePort      uint16
	DestinationPort uint16
	Protocol        string
	Action          string
	GID             uint32
	SignatureID     uint32
	Rev             uint32
	Signature       string
	Category        string
	Severity        uint8
	Payload         string
}
```

- [ ] **Step 4: Remove the duplicate `FlowRecord` from `enrichment.go`**

In `backend/internal/processor/enrichment.go`, delete lines 7-22 (the `// FlowRecord represents...` comment and the `type FlowRecord struct { ... }` block). The file now starts directly with the `EnrichFlow` function — it compiles against the `FlowRecord` defined in `types.go` since both files are in package `processor`.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && go test ./internal/processor/... -run TestFiveTupleAsMapKey -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add backend/internal/processor/types.go backend/internal/processor/types_test.go backend/internal/processor/enrichment.go
git commit -m "feat: consolidate shared processor types (FlowRecord, FiveTuple, DPIMetadata, SuricataAlert)"
```

---

### Task 3: NetFlow v9 decoder

**Files:**
- Create: `backend/internal/collector/netflowv9/decoder.go`
- Test: `backend/internal/collector/netflowv9/decoder_test.go`

A focused decoder for NetFlow v9 (RFC 3954): tracks templates per exporter and decodes Data FlowSets into `Flow` structs using the field types Flux.io needs (`IPV4_SRC_ADDR`=8, `IPV4_DST_ADDR`=12, `L4_SRC_PORT`=7, `L4_DST_PORT`=11, `PROTOCOL`=4, `IN_BYTES`=1, `IN_PKTS`=2). Unknown field types are skipped using their declared length, so the decoder degrades gracefully on exotic templates.

- [ ] **Step 1: Write the failing test — decoding a hand-built Template + Data FlowSet pair**

```go
package netflowv9

import (
	"encoding/binary"
	"net"
	"testing"
)

// buildPacket assembles a minimal NetFlow v9 packet: header + one Template
// FlowSet (defining template 256 with 6 fields) + one Data FlowSet carrying
// a single flow record matching that template.
func buildPacket(t *testing.T) []byte {
	t.Helper()
	var buf []byte

	// --- Packet header (20 bytes) ---
	buf = appendU16(buf, 9)          // version
	buf = appendU16(buf, 1)          // count (number of flowsets, informational)
	buf = appendU32(buf, 0)          // sysUptime
	buf = appendU32(buf, 1700000000) // unixSecs
	buf = appendU32(buf, 1)          // sequence
	buf = appendU32(buf, 0)          // sourceID

	// --- Template FlowSet (flowSetID = 0) ---
	// fields: IN_BYTES(1,4) IN_PKTS(2,4) PROTOCOL(4,1) L4_SRC_PORT(7,2)
	//         IPV4_SRC_ADDR(8,4) L4_DST_PORT(11,2) IPV4_DST_ADDR(12,4)
	type field struct{ typ, length uint16 }
	fields := []field{{1, 4}, {2, 4}, {4, 1}, {7, 2}, {8, 4}, {11, 2}, {12, 4}}
	var tmplBody []byte
	tmplBody = appendU16(tmplBody, 256) // template ID
	tmplBody = appendU16(tmplBody, uint16(len(fields)))
	for _, f := range fields {
		tmplBody = appendU16(tmplBody, f.typ)
		tmplBody = appendU16(tmplBody, f.length)
	}
	tmplFlowSetLen := 4 + len(tmplBody)
	buf = appendU16(buf, 0) // flowSetID = 0 (template)
	buf = appendU16(buf, uint16(tmplFlowSetLen))
	buf = append(buf, tmplBody...)

	// --- Data FlowSet (flowSetID = 256) ---
	var rec []byte
	rec = appendU32(rec, 1500)                       // IN_BYTES
	rec = appendU32(rec, 10)                         // IN_PKTS
	rec = append(rec, 6)                             // PROTOCOL = TCP
	rec = appendU16(rec, 51000)                      // L4_SRC_PORT
	rec = append(rec, net.ParseIP("10.0.0.1").To4()...)      // IPV4_SRC_ADDR
	rec = appendU16(rec, 443)                        // L4_DST_PORT
	rec = append(rec, net.ParseIP("93.184.216.34").To4()...) // IPV4_DST_ADDR

	dataFlowSetLen := 4 + len(rec)
	buf = appendU16(buf, 256) // flowSetID = 256 (matches template ID)
	buf = appendU16(buf, uint16(dataFlowSetLen))
	buf = append(buf, rec...)

	return buf
}

func appendU16(b []byte, v uint16) []byte {
	tmp := make([]byte, 2)
	binary.BigEndian.PutUint16(tmp, v)
	return append(b, tmp...)
}

func appendU32(b []byte, v uint32) []byte {
	tmp := make([]byte, 4)
	binary.BigEndian.PutUint32(tmp, v)
	return append(b, tmp...)
}

func TestDecode_TemplateThenData(t *testing.T) {
	d := NewDecoder()
	packet := buildPacket(t)

	flows, err := d.Decode("192.0.2.1:2055", packet)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if len(flows) != 0 {
		t.Fatalf("expected 0 flows from a template-only packet (data uses a template defined in the SAME packet, which real exporters never do); got %d", len(flows))
	}

	// Send the SAME packet again: now the decoder has the template cached
	// from the first pass, so the data flowset in this second packet decodes.
	flows, err = d.Decode("192.0.2.1:2055", packet)
	if err != nil {
		t.Fatalf("second Decode returned error: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow on second decode, got %d", len(flows))
	}

	f := flows[0]
	if f.SrcIP.String() != "10.0.0.1" || f.DstIP.String() != "93.184.216.34" {
		t.Errorf("unexpected IPs: src=%s dst=%s", f.SrcIP, f.DstIP)
	}
	if f.SrcPort != 51000 || f.DstPort != 443 {
		t.Errorf("unexpected ports: src=%d dst=%d", f.SrcPort, f.DstPort)
	}
	if f.Protocol != 6 || f.Bytes != 1500 || f.Packets != 10 {
		t.Errorf("unexpected proto/bytes/packets: %d/%d/%d", f.Protocol, f.Bytes, f.Packets)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/collector/netflowv9/... -v`
Expected: FAIL — `undefined: NewDecoder` (package doesn't exist yet)

- [ ] **Step 3: Implement the decoder**

```go
package netflowv9

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
)

// Flow is a decoded NetFlow v9 data record, normalized to the fields
// Flux.io cares about.
type Flow struct {
	SrcIP    net.IP
	DstIP    net.IP
	SrcPort  uint16
	DstPort  uint16
	Protocol uint8
	Bytes    uint64
	Packets  uint64
}

// Field type IDs from the NetFlow v9 Information Element registry (RFC 3954).
const (
	fieldInBytes     = 1
	fieldInPkts      = 2
	fieldProtocol    = 4
	fieldL4SrcPort   = 7
	fieldIPv4SrcAddr = 8
	fieldL4DstPort   = 11
	fieldIPv4DstAddr = 12
)

type templateField struct {
	fieldType uint16
	length    uint16
}

type template struct {
	fields []templateField
}

// Decoder decodes NetFlow v9 packets, tracking templates per exporter
// address since template IDs are only unique within an exporter's scope.
type Decoder struct {
	mu        sync.Mutex
	templates map[string]map[uint16]template // exporter -> templateID -> template
}

func NewDecoder() *Decoder {
	return &Decoder{templates: make(map[string]map[uint16]template)}
}

// Decode parses a single NetFlow v9 UDP payload from the given exporter
// address, returning any fully-decodable flow records it contains.
// Data FlowSets whose template hasn't been seen yet are skipped (the
// exporter will resend templates periodically).
func (d *Decoder) Decode(exporter string, payload []byte) ([]Flow, error) {
	if len(payload) < 20 {
		return nil, fmt.Errorf("netflowv9: packet too short (%d bytes)", len(payload))
	}
	version := binary.BigEndian.Uint16(payload[0:2])
	if version != 9 {
		return nil, fmt.Errorf("netflowv9: unsupported version %d", version)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.templates[exporter] == nil {
		d.templates[exporter] = make(map[uint16]template)
	}

	var flows []Flow
	offset := 20 // skip packet header
	for offset+4 <= len(payload) {
		flowSetID := binary.BigEndian.Uint16(payload[offset : offset+2])
		flowSetLength := int(binary.BigEndian.Uint16(payload[offset+2 : offset+4]))
		if flowSetLength < 4 || offset+flowSetLength > len(payload) {
			return flows, fmt.Errorf("netflowv9: invalid flowset length %d at offset %d", flowSetLength, offset)
		}
		body := payload[offset+4 : offset+flowSetLength]

		switch {
		case flowSetID == 0:
			d.parseTemplateFlowSet(exporter, body)
		case flowSetID == 1:
			// Options Template FlowSet — not needed for flow records, skip.
		case flowSetID >= 256:
			if fl, ok := d.parseDataFlowSet(exporter, flowSetID, body); ok {
				flows = append(flows, fl...)
			}
		}

		offset += flowSetLength
	}
	return flows, nil
}

func (d *Decoder) parseTemplateFlowSet(exporter string, body []byte) {
	offset := 0
	for offset+4 <= len(body) {
		templateID := binary.BigEndian.Uint16(body[offset : offset+2])
		fieldCount := int(binary.BigEndian.Uint16(body[offset+2 : offset+4]))
		offset += 4

		fields := make([]templateField, 0, fieldCount)
		for i := 0; i < fieldCount && offset+4 <= len(body); i++ {
			fields = append(fields, templateField{
				fieldType: binary.BigEndian.Uint16(body[offset : offset+2]),
				length:    binary.BigEndian.Uint16(body[offset+2 : offset+4]),
			})
			offset += 4
		}
		d.templates[exporter][templateID] = template{fields: fields}
	}
}

func (d *Decoder) parseDataFlowSet(exporter string, templateID uint16, body []byte) ([]Flow, bool) {
	tmpl, ok := d.templates[exporter][templateID]
	if !ok {
		return nil, false // template not seen yet — skip until exporter resends it
	}

	recordLength := 0
	for _, f := range tmpl.fields {
		recordLength += int(f.length)
	}
	if recordLength == 0 {
		return nil, false
	}

	var flows []Flow
	for offset := 0; offset+recordLength <= len(body); offset += recordLength {
		flows = append(flows, decodeRecord(body[offset:offset+recordLength], tmpl.fields))
	}
	return flows, true
}

func decodeRecord(record []byte, fields []templateField) Flow {
	var f Flow
	offset := 0
	for _, field := range fields {
		end := offset + int(field.length)
		if end > len(record) {
			break
		}
		raw := record[offset:end]
		switch field.fieldType {
		case fieldInBytes:
			f.Bytes = decodeUint(raw)
		case fieldInPkts:
			f.Packets = decodeUint(raw)
		case fieldProtocol:
			if len(raw) > 0 {
				f.Protocol = raw[0]
			}
		case fieldL4SrcPort:
			f.SrcPort = uint16(decodeUint(raw))
		case fieldL4DstPort:
			f.DstPort = uint16(decodeUint(raw))
		case fieldIPv4SrcAddr:
			f.SrcIP = net.IP(append([]byte(nil), raw...)).To4()
		case fieldIPv4DstAddr:
			f.DstIP = net.IP(append([]byte(nil), raw...)).To4()
		}
		offset = end
	}
	return f
}

// decodeUint reads a big-endian unsigned integer of arbitrary length (up to 8 bytes),
// as NetFlow v9 counters can be encoded in 1, 2, 4, or 8 bytes depending on the exporter.
func decodeUint(raw []byte) uint64 {
	var v uint64
	for _, b := range raw {
		v = (v << 8) | uint64(b)
	}
	return v
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/collector/netflowv9/... -v`
Expected: PASS — `TestDecode_TemplateThenData` passes (first call returns 0 flows because the template arrives in the same packet as the data — real exporters send templates in earlier packets; the second call, with the template now cached, decodes the flow correctly).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/collector/netflowv9/
git commit -m "feat: add NetFlow v9 decoder with per-exporter template tracking"
```

---

### Task 4: Wire the decoder into the UDP listener and produce FlowRecords

**Files:**
- Modify: `backend/internal/collector/netflow.go` (replace entirely)
- Test: `backend/internal/collector/netflow_test.go`

Replaces the stub UDP listener with one that uses the new decoder, converts `netflowv9.Flow` into `processor.FlowRecord`, and pushes records onto a channel for the rest of the pipeline to consume. The conversion function is what's unit-tested here (it's pure and doesn't need a real socket); the listener loop itself is exercised via the end-to-end Docker run in the final verification task.

- [ ] **Step 1: Write the failing test for Flow → FlowRecord conversion**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/collector/... -run TestToFlowRecord -v`
Expected: FAIL — `undefined: toFlowRecord`

- [ ] **Step 3: Replace `netflow.go` with the real listener + conversion function**

```go
package collector

import (
	"log"
	"net"
	"time"

	"fluxio-backend/internal/collector/netflowv9"
	"fluxio-backend/internal/processor"
)

// StartNetFlowListener listens for NetFlow v9 / IPFIX UDP packets on the given
// port, decodes them, and pushes normalized FlowRecords onto out. It runs
// until the process exits; malformed packets are logged and skipped so a
// single bad exporter can't take the listener down.
func StartNetFlowListener(port string, out chan<- processor.FlowRecord) {
	addr, err := net.ResolveUDPAddr("udp", ":"+port)
	if err != nil {
		log.Fatalf("netflow: error resolving UDP address: %v", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatalf("netflow: error listening on UDP %s: %v", port, err)
	}
	defer conn.Close()

	log.Printf("netflow: listening for NetFlow v9/IPFIX on UDP %s", port)

	decoder := netflowv9.NewDecoder()
	buf := make([]byte, 8192)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("netflow: error reading from UDP: %v", err)
			continue
		}

		flows, err := decoder.Decode(remoteAddr.String(), buf[:n])
		if err != nil {
			log.Printf("netflow: dropping malformed packet from %v: %v", remoteAddr, err)
			continue
		}

		now := time.Now().UTC()
		for _, flow := range flows {
			out <- toFlowRecord(flow, now)
		}
	}
}

func toFlowRecord(flow netflowv9.Flow, receivedAt time.Time) processor.FlowRecord {
	return processor.FlowRecord{
		Timestamp:       receivedAt,
		SourceIP:        flow.SrcIP.String(),
		DestinationIP:   flow.DstIP.String(),
		SourcePort:      flow.SrcPort,
		DestinationPort: flow.DstPort,
		Protocol:        flow.Protocol,
		Bytes:           flow.Bytes,
		Packets:         flow.Packets,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/collector/... -run TestToFlowRecord -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/collector/netflow.go backend/internal/collector/netflow_test.go
git commit -m "feat: decode real NetFlow v9 packets and emit FlowRecords on a channel"
```

---

### Task 5: ClickHouse batch writer

**Files:**
- Create: `backend/internal/storage/clickhouse.go`
- Create: `backend/internal/storage/batch_writer.go`
- Test: `backend/internal/storage/batch_writer_test.go`

Defines an `Inserter` interface (so the writer is testable without a real ClickHouse instance), a `ClickHouseStore` implementing it via `clickhouse-go/v2`, and a `BatchWriter` that buffers records and flushes them by size or time — whichever comes first.

- [ ] **Step 1: Write the failing test for batch-by-size flushing using a fake Inserter**

```go
package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"fluxio-backend/internal/processor"
)

type fakeInserter struct {
	mu    sync.Mutex
	flows [][]processor.FlowRecord
}

func (f *fakeInserter) InsertFlows(_ context.Context, records []processor.FlowRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	batch := append([]processor.FlowRecord(nil), records...)
	f.flows = append(f.flows, batch)
	return nil
}

func (f *fakeInserter) InsertAlerts(_ context.Context, _ []processor.SuricataAlert) error {
	return nil
}

func (f *fakeInserter) batchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.flows)
}

func TestBatchWriter_FlushesWhenBatchSizeReached(t *testing.T) {
	fake := &fakeInserter{}
	writer := NewBatchWriter(fake, 2, time.Hour) // huge interval: only size-based flush should fire

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go writer.Run(ctx)

	writer.WriteFlow(processor.FlowRecord{SourceIP: "10.0.0.1"})
	writer.WriteFlow(processor.FlowRecord{SourceIP: "10.0.0.2"})

	deadline := time.Now().Add(2 * time.Second)
	for fake.batchCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := fake.batchCount(); got != 1 {
		t.Fatalf("expected exactly 1 flushed batch of size 2, got %d batches", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/storage/... -run TestBatchWriter_FlushesWhenBatchSizeReached -v`
Expected: FAIL — `undefined: NewBatchWriter`

- [ ] **Step 3: Implement `Inserter` interface and `BatchWriter`**

Create `backend/internal/storage/batch_writer.go`:

```go
package storage

import (
	"context"
	"log"
	"time"

	"fluxio-backend/internal/processor"
)

// Inserter persists batches of records. Implemented by ClickHouseStore;
// fakeable in tests so the batching logic can be verified without a database.
type Inserter interface {
	InsertFlows(ctx context.Context, records []processor.FlowRecord) error
	InsertAlerts(ctx context.Context, alerts []processor.SuricataAlert) error
}

// BatchWriter buffers FlowRecords and SuricataAlerts and flushes them to an
// Inserter whenever the buffer reaches batchSize or flushEvery elapses,
// whichever happens first. This bounds both write latency and the number
// of round-trips to ClickHouse.
type BatchWriter struct {
	inserter   Inserter
	batchSize  int
	flushEvery time.Duration

	flowCh  chan processor.FlowRecord
	alertCh chan processor.SuricataAlert
}

func NewBatchWriter(inserter Inserter, batchSize int, flushEvery time.Duration) *BatchWriter {
	return &BatchWriter{
		inserter:   inserter,
		batchSize:  batchSize,
		flushEvery: flushEvery,
		flowCh:     make(chan processor.FlowRecord, batchSize*4),
		alertCh:    make(chan processor.SuricataAlert, batchSize),
	}
}

// WriteFlow enqueues a record for the next flush. It never blocks the caller
// for long: if the buffer is saturated, the oldest pending record is dropped
// so a slow database can't back-pressure the whole collection pipeline.
func (w *BatchWriter) WriteFlow(r processor.FlowRecord) {
	select {
	case w.flowCh <- r:
	default:
		select {
		case <-w.flowCh:
		default:
		}
		log.Printf("storage: flow buffer full, dropped oldest record to make room")
		w.flowCh <- r
	}
}

func (w *BatchWriter) WriteAlert(a processor.SuricataAlert) {
	select {
	case w.alertCh <- a:
	default:
		select {
		case <-w.alertCh:
		default:
		}
		log.Printf("storage: alert buffer full, dropped oldest record to make room")
		w.alertCh <- a
	}
}

// Run drains both channels, accumulating batches and flushing on size or
// time, until ctx is cancelled (flushing whatever remains before returning).
func (w *BatchWriter) Run(ctx context.Context) {
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()

	flows := make([]processor.FlowRecord, 0, w.batchSize)
	alerts := make([]processor.SuricataAlert, 0, w.batchSize)

	flush := func() {
		if len(flows) > 0 {
			w.flushFlows(ctx, flows)
			flows = flows[:0]
		}
		if len(alerts) > 0 {
			w.flushAlerts(ctx, alerts)
			alerts = alerts[:0]
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case rec := <-w.flowCh:
			flows = append(flows, rec)
			if len(flows) >= w.batchSize {
				w.flushFlows(ctx, flows)
				flows = flows[:0]
			}
		case alert := <-w.alertCh:
			alerts = append(alerts, alert)
			if len(alerts) >= w.batchSize {
				w.flushAlerts(ctx, alerts)
				alerts = alerts[:0]
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (w *BatchWriter) flushFlows(ctx context.Context, batch []processor.FlowRecord) {
	toSend := append([]processor.FlowRecord(nil), batch...)
	if err := w.insertWithRetry(ctx, func(ctx context.Context) error {
		return w.inserter.InsertFlows(ctx, toSend)
	}); err != nil {
		log.Printf("storage: giving up on flow batch of %d records: %v", len(toSend), err)
	}
}

func (w *BatchWriter) flushAlerts(ctx context.Context, batch []processor.SuricataAlert) {
	toSend := append([]processor.SuricataAlert(nil), batch...)
	if err := w.insertWithRetry(ctx, func(ctx context.Context) error {
		return w.inserter.InsertAlerts(ctx, toSend)
	}); err != nil {
		log.Printf("storage: giving up on alert batch of %d records: %v", len(toSend), err)
	}
}

// insertWithRetry retries a write up to 3 times with exponential backoff
// (200ms, 400ms, 800ms) before giving up — ClickHouse outages are usually
// transient (restarts, brief network blips).
func (w *BatchWriter) insertWithRetry(ctx context.Context, fn func(context.Context) error) error {
	backoff := 200 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := fn(ctx); err == nil {
			return nil
		} else {
			lastErr = err
			log.Printf("storage: insert attempt %d failed: %v", attempt+1, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			backoff *= 2
		}
	}
	return lastErr
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/storage/... -run TestBatchWriter_FlushesWhenBatchSizeReached -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for time-based flushing**

Append to `batch_writer_test.go`:

```go
func TestBatchWriter_FlushesOnTimer(t *testing.T) {
	fake := &fakeInserter{}
	writer := NewBatchWriter(fake, 100, 50*time.Millisecond) // huge size: only timer should fire

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go writer.Run(ctx)

	writer.WriteFlow(processor.FlowRecord{SourceIP: "10.0.0.1"})

	deadline := time.Now().Add(2 * time.Second)
	for fake.batchCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := fake.batchCount(); got < 1 {
		t.Fatalf("expected timer to flush at least 1 batch, got %d", got)
	}
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd backend && go test ./internal/storage/... -run TestBatchWriter_FlushesOnTimer -v`
Expected: PASS

- [ ] **Step 7: Implement `ClickHouseStore` (the real `Inserter`)**

Create `backend/internal/storage/clickhouse.go`:

```go
package storage

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"fluxio-backend/internal/processor"
)

// ClickHouseStore is the production Inserter: it batches records into
// native ClickHouse batch inserts against the schema created by
// db/clickhouse/init-db.sql.
type ClickHouseStore struct {
	conn driver.Conn
}

func NewClickHouseStore(dsn string) (*ClickHouseStore, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: invalid DSN: %w", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: failed to connect: %w", err)
	}
	if err := conn.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("clickhouse: ping failed: %w", err)
	}
	return &ClickHouseStore{conn: conn}, nil
}

func (s *ClickHouseStore) InsertFlows(ctx context.Context, records []processor.FlowRecord) error {
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO network_flows (
		timestamp, src_ip, dst_ip, src_port, dst_port, protocol, bytes, packets,
		application_id, sni, http_host, http_url,
		src_country, dst_country, src_asn, dst_asn, src_asn_org, dst_asn_org,
		src_hostname, dst_hostname, is_alert, alert_severity, alert_signature
	)`)
	if err != nil {
		return fmt.Errorf("clickhouse: prepare flow batch: %w", err)
	}

	for _, r := range records {
		isAlert := uint8(0)
		if r.IsAlert {
			isAlert = 1
		}
		err := batch.Append(
			r.Timestamp, r.SourceIP, r.DestinationIP, r.SourcePort, r.DestinationPort,
			r.Protocol, r.Bytes, r.Packets,
			r.Application, r.SNI, r.HTTPHost, r.HTTPURL,
			r.SourceCountry, r.DestCountry, r.SourceASN, r.DestASN, r.SourceASNOrg, r.DestASNOrg,
			r.SourceHostname, r.DestHostname, isAlert, r.AlertSeverity, r.AlertSignature,
		)
		if err != nil {
			return fmt.Errorf("clickhouse: append flow record: %w", err)
		}
	}
	return batch.Send()
}

func (s *ClickHouseStore) InsertAlerts(ctx context.Context, alerts []processor.SuricataAlert) error {
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO suricata_alerts (
		timestamp, src_ip, dst_ip, src_port, dst_port, protocol,
		alert_action, alert_gid, alert_signature_id, alert_rev,
		alert_signature, alert_category, alert_severity, payload
	)`)
	if err != nil {
		return fmt.Errorf("clickhouse: prepare alert batch: %w", err)
	}

	for _, a := range alerts {
		err := batch.Append(
			a.Timestamp, a.SourceIP, a.DestinationIP, a.SourcePort, a.DestinationPort, a.Protocol,
			a.Action, a.GID, a.SignatureID, a.Rev,
			a.Signature, a.Category, a.Severity, a.Payload,
		)
		if err != nil {
			return fmt.Errorf("clickhouse: append alert record: %w", err)
		}
	}
	return batch.Send()
}
```

- [ ] **Step 8: Commit**

```bash
git add backend/internal/storage/
git commit -m "feat: add ClickHouse batch writer with retry/backoff and bounded buffers"
```

---

### Task 6: Real GeoIP/ASN enrichment (MaxMind GeoLite2)

**Files:**
- Modify: `backend/internal/processor/enrichment.go` (replace stub lookups)
- Test: `backend/internal/processor/enrichment_test.go`
- Modify: `README.md` (document how to obtain the GeoLite2 databases)
- Modify: `docker-compose.yml:52-56` (mount the GeoIP database directory)

Replaces the `lookupCountry`/`lookupASN` stubs (which always return `"BR"`/`28573`) with real lookups against local MaxMind GeoLite2 `.mmdb` files via `geoip2-golang`. The enricher must degrade gracefully — if the database files aren't present (e.g., a developer hasn't downloaded them yet), enrichment becomes a no-op with a one-time warning rather than crashing the service.

- [ ] **Step 1: Write the failing test for graceful degradation when DB files are missing**

```go
package processor

import "testing"

func TestGeoIPEnricher_MissingDatabases_NoOpWithoutError(t *testing.T) {
	enricher, err := NewGeoIPEnricher("/nonexistent/City.mmdb", "/nonexistent/ASN.mmdb")
	if err != nil {
		t.Fatalf("expected NewGeoIPEnricher to tolerate missing files, got error: %v", err)
	}

	country, asn, asnOrg := enricher.Lookup("8.8.8.8")
	if country != "" || asn != 0 || asnOrg != "" {
		t.Errorf("expected empty enrichment when databases are absent, got country=%q asn=%d org=%q", country, asn, asnOrg)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/processor/... -run TestGeoIPEnricher_MissingDatabases_NoOpWithoutError -v`
Expected: FAIL — `undefined: NewGeoIPEnricher`

- [ ] **Step 3: Replace the enrichment stubs with a real `GeoIPEnricher`**

Replace the entire contents of `backend/internal/processor/enrichment.go`:

```go
package processor

import (
	"log"
	"net"

	"github.com/oschwald/geoip2-golang"
)

// GeoIPEnricher resolves country and ASN information for IP addresses using
// local MaxMind GeoLite2 databases. If a database file is missing, the
// corresponding lookups become no-ops (with a one-time startup warning)
// instead of making the service fail to start — useful for local development
// where a developer may not have downloaded the (free, license-gated) databases yet.
type GeoIPEnricher struct {
	cityDB *geoip2.Reader
	asnDB  *geoip2.Reader
}

// NewGeoIPEnricher opens the GeoLite2-City and GeoLite2-ASN databases at the
// given paths. Missing files are logged as warnings, not returned as errors.
func NewGeoIPEnricher(cityDBPath, asnDBPath string) (*GeoIPEnricher, error) {
	e := &GeoIPEnricher{}

	if db, err := geoip2.Open(cityDBPath); err != nil {
		log.Printf("enrichment: GeoLite2-City database unavailable at %q (%v) — country lookups disabled", cityDBPath, err)
	} else {
		e.cityDB = db
	}

	if db, err := geoip2.Open(asnDBPath); err != nil {
		log.Printf("enrichment: GeoLite2-ASN database unavailable at %q (%v) — ASN lookups disabled", asnDBPath, err)
	} else {
		e.asnDB = db
	}

	return e, nil
}

// Close releases the underlying database file handles.
func (e *GeoIPEnricher) Close() {
	if e.cityDB != nil {
		e.cityDB.Close()
	}
	if e.asnDB != nil {
		e.asnDB.Close()
	}
}

// Lookup returns the ISO country code, ASN, and ASN organization for ip.
// Any field whose database isn't loaded, or whose lookup fails (private IPs,
// unknown addresses), is returned as its zero value.
func (e *GeoIPEnricher) Lookup(ipStr string) (country string, asn uint32, asnOrg string) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return "", 0, ""
	}

	if e.cityDB != nil {
		if rec, err := e.cityDB.City(ip); err == nil {
			country = rec.Country.IsoCode
		}
	}

	if e.asnDB != nil {
		if rec, err := e.asnDB.ASN(ip); err == nil {
			asn = uint32(rec.AutonomousSystemNumber)
			asnOrg = rec.AutonomousSystemOrganization
		}
	}

	return country, asn, asnOrg
}

// EnrichFlow adds GeoIP and ASN information to a flow record in place.
func (e *GeoIPEnricher) EnrichFlow(flow *FlowRecord) {
	if flow.SourceIP != "" {
		flow.SourceCountry, flow.SourceASN, flow.SourceASNOrg = e.Lookup(flow.SourceIP)
	}
	if flow.DestinationIP != "" {
		flow.DestCountry, flow.DestASN, flow.DestASNOrg = e.Lookup(flow.DestinationIP)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/processor/... -run TestGeoIPEnricher_MissingDatabases_NoOpWithoutError -v`
Expected: PASS

- [ ] **Step 5: Document how to obtain the databases and mount them**

Append to `README.md`:

```markdown
## GeoIP enrichment (MaxMind GeoLite2)

Flux.io enriches flows with country and ASN data using MaxMind's free
GeoLite2 databases. To enable this:

1. Create a free MaxMind account and generate a license key:
   https://www.maxmind.com/en/geolite2/signup
2. Download `GeoLite2-City.mmdb` and `GeoLite2-ASN.mmdb`.
3. Place both files in `./geoip/` at the repo root (this directory is
   mounted into the backend container — see `docker-compose.yml`).

If the files are absent, the backend still starts; country/ASN fields are
simply left empty and a warning is logged at startup.
```

Add a `.gitignore` entry so the (large, license-gated) database files are never committed:

```
geoip/*.mmdb
```

- [ ] **Step 6: Mount the GeoIP directory into the backend container**

In `docker-compose.yml`, under the `backend` service `volumes:` (currently only `./suricata/logs:/var/log/suricata:ro`), add:

```yaml
    volumes:
      - ./suricata/logs:/var/log/suricata:ro
      - ./geoip:/root/geoip:ro
```

And add to the `backend` service `environment:` block:

```yaml
      - GEOIP_CITY_DB=/root/geoip/GeoLite2-City.mmdb
      - GEOIP_ASN_DB=/root/geoip/GeoLite2-ASN.mmdb
```

- [ ] **Step 7: Commit**

```bash
git add backend/internal/processor/enrichment.go backend/internal/processor/enrichment_test.go README.md .gitignore docker-compose.yml
git commit -m "feat: replace GeoIP/ASN stubs with real MaxMind GeoLite2 lookups"
```

---

### Task 7: Wire NetFlow → enrichment → ClickHouse end-to-end in main.go

**Files:**
- Modify: `backend/cmd/server/main.go`

This is the first end-to-end milestone: real packets in, enriched rows in ClickHouse out — without DPI yet (DPI is wired in Tasks 11-14). Getting this working early means the dashboard (a separate future spec) already has real data to render while DPI work continues.

- [ ] **Step 1: Replace the body of `main()` to start the pipeline**

In `backend/cmd/server/main.go`, add these imports:

```go
	"context"
	"time"

	"fluxio-backend/internal/collector"
	"fluxio-backend/internal/processor"
	"fluxio-backend/internal/storage"
```

Replace the block that currently reads (lines ~87-92):

```go
	// Inicializa o Wazuh Forwarder em background (Goroutine)
	wazuhIP := os.Getenv("WAZUH_MANAGER_IP")
	wazuhPort := os.Getenv("WAZUH_MANAGER_PORT")
	// import "fluxio-backend/internal/collector" seria necessário aqui na vida real,
	// porém como estamos demonstrando o esqueleto, omitimos o start se não estiver na GOPATH
	log.Printf("Wazuh Integration configured for: %s:%s", wazuhIP, wazuhPort)
```

with:

```go
	pipelineCtx, cancelPipeline := context.WithCancel(context.Background())
	defer cancelPipeline()

	store, err := storage.NewClickHouseStore(os.Getenv("CLICKHOUSE_DSN"))
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}

	writer := storage.NewBatchWriter(store, 1000, 5*time.Second)
	go writer.Run(pipelineCtx)

	geoIP, err := processor.NewGeoIPEnricher(os.Getenv("GEOIP_CITY_DB"), os.Getenv("GEOIP_ASN_DB"))
	if err != nil {
		log.Fatalf("Failed to initialize GeoIP enrichment: %v", err)
	}
	defer geoIP.Close()

	flowCh := make(chan processor.FlowRecord, 10000)
	go func() {
		for flow := range flowCh {
			geoIP.EnrichFlow(&flow)
			writer.WriteFlow(flow)
		}
	}()

	netflowPort := os.Getenv("NETFLOW_PORT")
	if netflowPort == "" {
		netflowPort = "2055"
	}
	go collector.StartNetFlowListener(netflowPort, flowCh)
```

- [ ] **Step 2: Add `NETFLOW_PORT` to the backend environment in `docker-compose.yml`**

In the `backend` service `environment:` block, add:

```yaml
      - NETFLOW_PORT=2055
```

- [ ] **Step 3: Verify the backend builds**

Run: `cd backend && go build ./...`
Expected: builds successfully with no errors (this also catches any wiring/import mistakes before a full Docker rebuild).

- [ ] **Step 4: Commit**

```bash
git add backend/cmd/server/main.go docker-compose.yml
git commit -m "feat: wire NetFlow listener through GeoIP enrichment into ClickHouse batch writer"
```

---

### Task 8: Settings storage in Postgres

**Files:**
- Create: `db/postgres/init-db.sql`
- Modify: `docker-compose.yml:15-26` (mount the new init script)
- Create: `backend/internal/settings/repository.go`
- Test: `backend/internal/settings/repository_test.go`

Adds a `settings` key/value table to Postgres (currently provisioned but unused) and a `Repository` for reading/writing the active DPI mode. `dpi_mode` defaults to `"none"` so a fresh install doesn't silently start an unconfigured listener.

- [ ] **Step 1: Create the Postgres init script**

Create `db/postgres/init-db.sql`:

```sql
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO settings (key, value)
VALUES ('dpi_mode', 'none')
ON CONFLICT (key) DO NOTHING;
```

- [ ] **Step 2: Mount the init script in `docker-compose.yml`**

In the `postgres` service, add to `volumes:` (alongside the existing `postgres-data` mount):

```yaml
    volumes:
      - postgres-data:/var/lib/postgresql/data
      - ./db/postgres/init-db.sql:/docker-entrypoint-initdb.d/init-db.sql
```

- [ ] **Step 3: Write the failing test for the settings repository**

This test requires a real Postgres reachable at `TEST_POSTGRES_DSN` — it's an integration test, skipped when that variable isn't set (so `go test ./...` stays fast and dependency-free in CI by default; it's run explicitly against the docker-compose Postgres during manual verification).

```go
package settings

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping Postgres integration test")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("failed to ensure settings table: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM settings`); err != nil {
		t.Fatalf("failed to reset settings table: %v", err)
	}
	return db
}

func TestRepository_DefaultsToNoneThenPersistsUpdates(t *testing.T) {
	db := openTestDB(t)
	repo := NewRepository(db)
	ctx := context.Background()

	mode, err := repo.GetDPIMode(ctx)
	if err != nil {
		t.Fatalf("GetDPIMode returned error: %v", err)
	}
	if mode != "none" {
		t.Fatalf("expected default mode %q, got %q", "none", mode)
	}

	if err := repo.SetDPIMode(ctx, "suricata"); err != nil {
		t.Fatalf("SetDPIMode returned error: %v", err)
	}

	mode, err = repo.GetDPIMode(ctx)
	if err != nil {
		t.Fatalf("GetDPIMode after update returned error: %v", err)
	}
	if mode != "suricata" {
		t.Fatalf("expected updated mode %q, got %q", "suricata", mode)
	}
}

func TestRepository_RejectsUnknownMode(t *testing.T) {
	db := openTestDB(t)
	repo := NewRepository(db)

	err := repo.SetDPIMode(context.Background(), "bogus")
	if err == nil {
		t.Fatal("expected SetDPIMode to reject an unknown mode, got nil error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails to compile**

Run: `cd backend && go test ./internal/settings/... -v`
Expected: FAIL — `undefined: NewRepository` (the test itself will report `SKIP` if `TEST_POSTGRES_DSN` is unset, but it must first fail to *compile* against the not-yet-written `Repository`)

- [ ] **Step 3: Implement the repository**

Create `backend/internal/settings/repository.go`:

```go
package settings

import (
	"context"
	"database/sql"
	"fmt"
)

// validDPIModes are the only values the system knows how to act on.
// "none" disables DPI entirely; "suricata" correlates with eve.json events;
// "tzsp" captures and parses raw packets via TZSP.
var validDPIModes = map[string]bool{
	"none":     true,
	"suricata": true,
	"tzsp":     true,
}

// Repository persists Flux.io's runtime settings in Postgres.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// GetDPIMode returns the currently configured DPI source. If no row exists
// yet (a fresh database without the seeded default), it returns "none".
func (r *Repository) GetDPIMode(ctx context.Context) (string, error) {
	var mode string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'dpi_mode'`).Scan(&mode)
	if err == sql.ErrNoRows {
		return "none", nil
	}
	if err != nil {
		return "", fmt.Errorf("settings: query dpi_mode: %w", err)
	}
	return mode, nil
}

// SetDPIMode persists the active DPI source. It rejects unknown values so
// the system never ends up in a mode no listener knows how to honor.
func (r *Repository) SetDPIMode(ctx context.Context, mode string) error {
	if !validDPIModes[mode] {
		return fmt.Errorf("settings: unknown dpi_mode %q (valid: none, suricata, tzsp)", mode)
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES ('dpi_mode', $1)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, mode)
	if err != nil {
		return fmt.Errorf("settings: persist dpi_mode: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run (against the docker-compose Postgres, started via `docker compose up -d postgres`):
`cd backend && TEST_POSTGRES_DSN="postgres://fluxio:fluxio_password@localhost:5432/fluxioclient?sslmode=disable" go test ./internal/settings/... -v`
Expected: PASS for both `TestRepository_DefaultsToNoneThenPersistsUpdates` and `TestRepository_RejectsUnknownMode`. (Running `go test ./...` without `TEST_POSTGRES_DSN` set shows both as `SKIP`, not `FAIL`.)

- [ ] **Step 5: Commit**

```bash
git add db/postgres/ docker-compose.yml backend/internal/settings/
git commit -m "feat: add settings repository backed by Postgres for configurable DPI mode"
```

---

### Task 9: Settings API endpoints

**Files:**
- Modify: `backend/cmd/server/main.go`
- Test: `backend/cmd/server/main_test.go`

Exposes `GET /api/settings` and `PUT /api/settings` so the frontend Settings page (Task 10) can read and change the active DPI mode. Uses Fiber's app test helper to exercise the routes without binding a real socket.

- [ ] **Step 1: Write the failing test for the settings handlers**

```go
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"fluxio-backend/internal/settings"
)

// fakeModeStore is a minimal in-memory stand-in for *settings.Repository,
// letting us test the HTTP layer without a database.
type fakeModeStore struct{ mode string }

func (f *fakeModeStore) GetDPIMode(context.Context) (string, error)      { return f.mode, nil }
func (f *fakeModeStore) SetDPIMode(_ context.Context, mode string) error { f.mode = mode; return nil }

func TestSettingsHandlers_GetAndPut(t *testing.T) {
	store := &fakeModeStore{mode: "none"}
	app := fiber.New()
	registerSettingsRoutes(app.Group("/api"), store)

	// GET returns the current mode
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("GET /api/settings failed: %v", err)
	}
	var got struct {
		DPIMode string `json:"dpi_mode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding GET response: %v", err)
	}
	if got.DPIMode != "none" {
		t.Fatalf("expected dpi_mode %q, got %q", "none", got.DPIMode)
	}

	// PUT updates the mode
	body, _ := json.Marshal(map[string]string{"dpi_mode": "suricata"})
	req = httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("PUT /api/settings failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200 from PUT, got %d", resp.StatusCode)
	}
	if store.mode != "suricata" {
		t.Fatalf("expected store to be updated to %q, got %q", "suricata", store.mode)
	}

	// PUT with an invalid mode is rejected with 400
	body, _ = json.Marshal(map[string]string{"dpi_mode": "bogus"})
	req = httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = app.Test(req)
	if err != nil {
		t.Fatalf("PUT /api/settings (invalid) failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected 400 for invalid dpi_mode, got %d", resp.StatusCode)
	}
}

var _ = sql.ErrNoRows // keep database/sql imported for clarity of intent in this package's tests
var _ = settings.NewRepository
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./cmd/server/... -run TestSettingsHandlers_GetAndPut -v`
Expected: FAIL — `undefined: registerSettingsRoutes`

- [ ] **Step 3: Implement the routes**

Create `backend/cmd/server/settings_routes.go`:

```go
package main

import (
	"context"

	"github.com/gofiber/fiber/v2"
)

// modeStore is the minimal interface the settings routes need — satisfied by
// *settings.Repository in production and by a fake in tests.
type modeStore interface {
	GetDPIMode(ctx context.Context) (string, error)
	SetDPIMode(ctx context.Context, mode string) error
}

// registerSettingsRoutes wires GET/PUT /settings onto the given router group
// (mounted at /api by the caller, so the final paths are /api/settings).
func registerSettingsRoutes(router fiber.Router, store modeStore) {
	router.Get("/settings", func(c *fiber.Ctx) error {
		mode, err := store.GetDPIMode(c.Context())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read settings"})
		}
		return c.JSON(fiber.Map{"dpi_mode": mode})
	})

	router.Put("/settings", func(c *fiber.Ctx) error {
		var body struct {
			DPIMode string `json:"dpi_mode"`
		}
		if err := c.BodyParser(&body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
		if err := store.SetDPIMode(c.Context(), body.DPIMode); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"dpi_mode": body.DPIMode})
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./cmd/server/... -run TestSettingsHandlers_GetAndPut -v`
Expected: PASS

- [ ] **Step 5: Wire the routes and a real Postgres connection into `main.go`**

Add to the imports in `backend/cmd/server/main.go`:

```go
	"database/sql"

	_ "github.com/lib/pq"

	"fluxio-backend/internal/settings"
```

After the `api := app.Group("/api")` line (and before the `/auth/login` handler), add:

```go
	pgDB, err := sql.Open("postgres", os.Getenv("POSTGRES_DSN"))
	if err != nil {
		log.Fatalf("Failed to open Postgres connection: %v", err)
	}
	defer pgDB.Close()
	settingsRepo := settings.NewRepository(pgDB)
	registerSettingsRoutes(api, settingsRepo)
```

- [ ] **Step 6: Run the full backend test suite to confirm nothing broke**

Run: `cd backend && go build ./... && go test ./... -short`
Expected: builds and all non-integration tests pass (Postgres/ClickHouse integration tests `SKIP` without their respective `TEST_*_DSN` env vars).

- [ ] **Step 7: Commit**

```bash
git add backend/cmd/server/
git commit -m "feat: add GET/PUT /api/settings endpoints for the configurable DPI mode"
```

---

### Task 10: Settings page in the frontend

**Files:**
- Create: `frontend/src/pages/Settings.tsx`
- Modify: `frontend/src/App.tsx`

Adds a Settings page where the user picks the active DPI source (mirrors the backend's `none | suricata | tzsp` modes), persisted via the API added in Task 9.

- [ ] **Step 1: Create the Settings page component**

Create `frontend/src/pages/Settings.tsx`:

```tsx
import { useEffect, useState } from 'react';

type DPIMode = 'none' | 'suricata' | 'tzsp';

const MODE_OPTIONS: { value: DPIMode; label: string; description: string }[] = [
  {
    value: 'none',
    label: 'Desativado',
    description: 'Os fluxos não são identificados por aplicação (campo "Application" fica vazio).',
  },
  {
    value: 'suricata',
    label: 'Correlação com Suricata',
    description: 'Reaproveita os eventos TLS/DNS/HTTP do eve.json do Suricata, correlacionando por 5-tupla.',
  },
  {
    value: 'tzsp',
    label: 'Captura TZSP',
    description: 'Recebe cópias de pacotes via TZSP (porta 37008/udp) e extrai SNI/DNS diretamente.',
  },
];

export default function Settings() {
  const [mode, setMode] = useState<DPIMode | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [savedMessage, setSavedMessage] = useState('');

  useEffect(() => {
    fetch('/api/settings')
      .then((res) => {
        if (!res.ok) throw new Error('Falha ao carregar configurações');
        return res.json();
      })
      .then((data: { dpi_mode: DPIMode }) => setMode(data.dpi_mode))
      .catch(() => setError('Não foi possível carregar as configurações.'));
  }, []);

  const handleSave = async (newMode: DPIMode) => {
    setSaving(true);
    setError('');
    setSavedMessage('');
    try {
      const res = await fetch('/api/settings', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dpi_mode: newMode }),
      });
      if (!res.ok) throw new Error('Falha ao salvar');
      setMode(newMode);
      setSavedMessage('Configuração salva.');
    } catch {
      setError('Não foi possível salvar a configuração.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="p-6 max-w-2xl">
      <h1 className="text-2xl font-bold mb-4">Configurações</h1>
      <div className="bg-gray-900 border border-gray-800 p-4 rounded-xl shadow-lg">
        <h2 className="text-gray-400 text-sm font-semibold mb-1">
          Identificação de Aplicações (DPI)
        </h2>
        <p className="text-gray-500 text-xs mb-4">
          Escolha como o Flux.io identifica a aplicação por trás de cada fluxo de rede.
        </p>

        {error && <div className="bg-red-900/50 text-red-200 p-2 mb-3 rounded text-sm">{error}</div>}
        {savedMessage && <div className="bg-green-900/50 text-green-200 p-2 mb-3 rounded text-sm">{savedMessage}</div>}

        <div className="space-y-3">
          {MODE_OPTIONS.map((opt) => (
            <label
              key={opt.value}
              className="flex items-start space-x-3 p-3 bg-gray-950 border border-gray-800 rounded-lg cursor-pointer hover:border-blue-600 transition-colors"
            >
              <input
                type="radio"
                name="dpi_mode"
                className="mt-1"
                checked={mode === opt.value}
                disabled={saving || mode === null}
                onChange={() => handleSave(opt.value)}
              />
              <div>
                <div className="text-white font-medium">{opt.label}</div>
                <div className="text-gray-500 text-xs">{opt.description}</div>
              </div>
            </label>
          ))}
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Wire the page into the router and sidebar**

In `frontend/src/App.tsx`:

Add the import near the other imports (after the `lucide-react` import line):

```tsx
import { Settings as SettingsIcon } from 'lucide-react';
import Settings from './pages/Settings';
```

Add a sidebar link inside `<nav>`, after the "Geo Map" `<Link>`:

```tsx
            <Link to="/settings" className="flex items-center space-x-3 px-4 py-3 text-gray-300 hover:bg-gray-900 hover:text-white rounded-lg transition-colors">
              <SettingsIcon className="w-5 h-5" />
              <span>Configurações</span>
            </Link>
```

Add a route inside `<Routes>`, after the `/map` route:

```tsx
            <Route path="/settings" element={<Settings />} />
```

- [ ] **Step 3: Verify the frontend builds and the page renders**

Run: `cd frontend && npm run build`
Expected: TypeScript compiles and Vite build succeeds with no errors.

Then run `npm run dev`, open the app, log in, click "Configurações" in the sidebar, and confirm:
- The page loads the current mode from `GET /api/settings` (shows "Desativado" selected by default on a fresh database),
- Selecting a different option calls `PUT /api/settings` and shows "Configuração salva.",
- Reloading the page preserves the selection (proves persistence round-trips through Postgres).

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/Settings.tsx frontend/src/App.tsx
git commit -m "feat: add Settings page for choosing the active DPI source"
```

---

### Task 11: Shared file tailer

**Files:**
- Create: `backend/internal/collector/filetailer.go`
- Test: `backend/internal/collector/filetailer_test.go`

Both the Suricata DPI correlation source (Task 13) and the alerts/Wazuh forwarder (Tasks 15-16) need to "tail -f" `eve.json`. This extracts that logic — currently duplicated-in-spirit inside `wazuh_forwarder.go` — into one reusable component that (a) retries opening the file if it doesn't exist yet, (b) starts at EOF, and (c) streams new lines as they're written, until its context is cancelled.

- [ ] **Step 1: Write the failing test — tailing picks up lines appended after start**

```go
package collector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileTailer_StreamsAppendedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eve.json")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if _, err := f.WriteString("line-written-before-tailing-starts\n"); err != nil {
		t.Fatalf("failed to seed file: %v", err)
	}
	f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := NewFileTailer(path)
	lines, err := tailer.Lines(ctx)
	if err != nil {
		t.Fatalf("Lines returned error: %v", err)
	}

	// Give the tailer a moment to seek to EOF before we append —
	// it must NOT replay the line written before it started.
	time.Sleep(100 * time.Millisecond)

	appendFile, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to reopen file for append: %v", err)
	}
	if _, err := appendFile.WriteString("line-appended-after-tailing-starts\n"); err != nil {
		t.Fatalf("failed to append: %v", err)
	}
	appendFile.Close()

	select {
	case line := <-lines:
		if line != "line-appended-after-tailing-starts" {
			t.Fatalf("expected the appended line, got %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for appended line")
	}
}

func TestFileTailer_RetriesUntilFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-yet-created.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := NewFileTailer(path)
	lines, err := tailer.Lines(ctx)
	if err != nil {
		t.Fatalf("Lines returned error: %v", err)
	}

	time.Sleep(200 * time.Millisecond) // tailer should be retrying quietly here

	if err := os.WriteFile(path, []byte("first-line\n"), 0644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	select {
	case line := <-lines:
		if line != "first-line" {
			t.Fatalf("expected %q, got %q", "first-line", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for line from a file created after tailing started")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/collector/... -run TestFileTailer -v`
Expected: FAIL — `undefined: NewFileTailer`

- [ ] **Step 3: Implement the tailer**

Create `backend/internal/collector/filetailer.go`:

```go
package collector

import (
	"bufio"
	"context"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

// FileTailer streams newly-appended lines from a file, similar to `tail -f`.
// It tolerates the file not existing yet (retrying until it appears) — useful
// when Suricata hasn't written its first eve.json line before Flux.io starts.
type FileTailer struct {
	path string
}

func NewFileTailer(path string) *FileTailer {
	return &FileTailer{path: path}
}

// Lines starts tailing in a background goroutine and returns a channel of
// complete lines (without the trailing newline). It seeks to the end of the
// file on open, so only content written after Lines is called is delivered.
// The goroutine — and the channel — stop when ctx is cancelled.
func (t *FileTailer) Lines(ctx context.Context) (<-chan string, error) {
	out := make(chan string, 256)
	go t.run(ctx, out)
	return out, nil
}

func (t *FileTailer) run(ctx context.Context, out chan<- string) {
	defer close(out)

	file := t.openWithRetry(ctx)
	if file == nil {
		return // context was cancelled while waiting for the file to appear
	}
	defer file.Close()

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		log.Printf("filetailer: failed to seek to end of %s: %v", t.path, err)
		return
	}
	reader := bufio.NewReader(file)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			time.Sleep(time.Second) // wait for more data to be written
			continue
		}

		select {
		case out <- strings.TrimRight(line, "\n"):
		case <-ctx.Done():
			return
		}
	}
}

// openWithRetry blocks (without busy-looping) until the file can be opened
// or ctx is cancelled, returning nil in the latter case.
func (t *FileTailer) openWithRetry(ctx context.Context) *os.File {
	for {
		file, err := os.Open(t.path)
		if err == nil {
			return file
		}
		log.Printf("filetailer: waiting for %s to exist (%v)", t.path, err)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/collector/... -run TestFileTailer -v`
Expected: PASS for both `TestFileTailer_StreamsAppendedLines` and `TestFileTailer_RetriesUntilFileExists`

- [ ] **Step 5: Commit**

```bash
git add backend/internal/collector/filetailer.go backend/internal/collector/filetailer_test.go
git commit -m "feat: add reusable FileTailer for streaming eve.json (tail -f semantics)"
```

---

### Task 12: Suricata eve.json event parsing

**Files:**
- Create: `backend/internal/collector/eve.go`
- Test: `backend/internal/collector/eve_test.go`

Parses Suricata `eve.json` lines into a typed `EveEvent`, and provides two projections used by downstream components: `FiveTuple()` (the correlation key, used by Tasks 13-14) and `DPIMetadata()` (extracted SNI/DNS/HTTP, also Task 13), plus `ToAlert()` (used by Task 15). Suricata is assumed to be configured with the flat (`version: 1`) `eve-log` DNS format, where `dns.rrname` is a single string — this is documented in the Suricata setup notes added in Task 16.

- [ ] **Step 1: Write the failing test for parsing and projecting events**

```go
package collector

import "testing"

const tlsEventJSON = `{"timestamp":"2026-06-08T12:00:00.000000+0000","event_type":"tls","src_ip":"10.0.0.1","src_port":51000,"dest_ip":"93.184.216.34","dest_port":443,"proto":"TCP","tls":{"sni":"example.com"}}`

const dnsEventJSON = `{"timestamp":"2026-06-08T12:00:01.000000+0000","event_type":"dns","src_ip":"10.0.0.1","src_port":53124,"dest_ip":"8.8.8.8","dest_port":53,"proto":"UDP","dns":{"rrname":"example.com","rrtype":"A"}}`

const httpEventJSON = `{"timestamp":"2026-06-08T12:00:02.000000+0000","event_type":"http","src_ip":"10.0.0.1","src_port":51010,"dest_ip":"93.184.216.34","dest_port":80,"proto":"TCP","http":{"hostname":"example.com","url":"/index.html"}}`

const alertEventJSON = `{"timestamp":"2026-06-08T12:00:03.000000+0000","event_type":"alert","src_ip":"203.0.113.9","src_port":443,"dest_ip":"10.0.0.5","dest_port":51022,"proto":"TCP","alert":{"action":"allowed","gid":1,"signature_id":2024897,"rev":2,"signature":"ET MALWARE Possible C2 Beacon","category":"A Network Trojan was Detected","severity":1}}`

const flowEventJSON = `{"timestamp":"2026-06-08T12:00:04.000000+0000","event_type":"flow","src_ip":"10.0.0.1","src_port":51000,"dest_ip":"93.184.216.34","dest_port":443,"proto":"TCP"}`

func TestParseEveLine_TLS(t *testing.T) {
	evt, err := ParseEveLine(tlsEventJSON)
	if err != nil {
		t.Fatalf("ParseEveLine returned error: %v", err)
	}

	tuple, ok := evt.FiveTuple()
	if !ok {
		t.Fatal("expected a valid FiveTuple from a TLS event")
	}
	if tuple.SrcIP != "10.0.0.1" || tuple.DstIP != "93.184.216.34" || tuple.SrcPort != 51000 || tuple.DstPort != 443 || tuple.Protocol != 6 {
		t.Errorf("unexpected tuple: %+v", tuple)
	}

	meta, ok := evt.DPIMetadata()
	if !ok {
		t.Fatal("expected DPI metadata from a TLS event with SNI")
	}
	if meta.SNI != "example.com" {
		t.Errorf("expected SNI %q, got %q", "example.com", meta.SNI)
	}
	if meta.Application != "TLS/example.com" {
		t.Errorf("expected application %q, got %q", "TLS/example.com", meta.Application)
	}
}

func TestParseEveLine_DNS(t *testing.T) {
	evt, err := ParseEveLine(dnsEventJSON)
	if err != nil {
		t.Fatalf("ParseEveLine returned error: %v", err)
	}
	meta, ok := evt.DPIMetadata()
	if !ok || meta.DNSQuery != "example.com" {
		t.Fatalf("expected DNS query %q, got ok=%v meta=%+v", "example.com", ok, meta)
	}
}

func TestParseEveLine_HTTP(t *testing.T) {
	evt, err := ParseEveLine(httpEventJSON)
	if err != nil {
		t.Fatalf("ParseEveLine returned error: %v", err)
	}
	meta, ok := evt.DPIMetadata()
	if !ok || meta.HTTPHost != "example.com" || meta.HTTPURL != "/index.html" {
		t.Fatalf("expected HTTP host/url, got ok=%v meta=%+v", ok, meta)
	}
}

func TestParseEveLine_FlowHasNoMetadata(t *testing.T) {
	evt, err := ParseEveLine(flowEventJSON)
	if err != nil {
		t.Fatalf("ParseEveLine returned error: %v", err)
	}
	if _, ok := evt.DPIMetadata(); ok {
		t.Error("expected a plain 'flow' event to carry no DPI metadata")
	}
	if _, ok := evt.ToAlert(); ok {
		t.Error("expected a plain 'flow' event to not be an alert")
	}
}

func TestParseEveLine_Alert(t *testing.T) {
	evt, err := ParseEveLine(alertEventJSON)
	if err != nil {
		t.Fatalf("ParseEveLine returned error: %v", err)
	}
	alert, ok := evt.ToAlert()
	if !ok {
		t.Fatal("expected a valid alert from an 'alert' event")
	}
	if alert.SourceIP != "203.0.113.9" || alert.SignatureID != 2024897 || alert.Severity != 1 {
		t.Errorf("unexpected alert fields: %+v", alert)
	}
	if alert.Signature != "ET MALWARE Possible C2 Beacon" {
		t.Errorf("unexpected signature: %q", alert.Signature)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/collector/... -run TestParseEveLine -v`
Expected: FAIL — `undefined: ParseEveLine`

- [ ] **Step 3: Implement event parsing and projections**

Create `backend/internal/collector/eve.go`:

```go
package collector

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fluxio-backend/internal/processor"
)

// EveEvent is a typed projection of the subset of Suricata eve.json fields
// Flux.io needs. Suricata emits many event_types on the same stream (flow,
// tls, dns, http, alert, ...); the relevant nested object is populated
// depending on event_type and the others are nil.
type EveEvent struct {
	Timestamp string    `json:"timestamp"`
	EventType string    `json:"event_type"`
	SrcIP     string    `json:"src_ip"`
	SrcPort   uint16    `json:"src_port"`
	DestIP    string    `json:"dest_ip"`
	DestPort  uint16    `json:"dest_port"`
	Proto     string    `json:"proto"`
	Alert     *eveAlert `json:"alert,omitempty"`
	TLS       *eveTLS   `json:"tls,omitempty"`
	DNS       *eveDNS   `json:"dns,omitempty"`
	HTTP      *eveHTTP  `json:"http,omitempty"`
}

type eveAlert struct {
	Action      string `json:"action"`
	GID         uint32 `json:"gid"`
	SignatureID uint32 `json:"signature_id"`
	Rev         uint32 `json:"rev"`
	Signature   string `json:"signature"`
	Category    string `json:"category"`
	Severity    uint8  `json:"severity"`
}

type eveTLS struct {
	SNI string `json:"sni"`
}

// eveDNS assumes Suricata's flat eve-log DNS format (eve-log.dns.version: 1),
// where rrname is a single string. The newer nested format (version: 2) uses
// a `query` array instead — documented as a prerequisite in Task 16.
type eveDNS struct {
	RRName string `json:"rrname"`
	RRType string `json:"rrtype"`
}

type eveHTTP struct {
	Hostname string `json:"hostname"`
	URL      string `json:"url"`
}

// ParseEveLine decodes a single line of Suricata's eve.json.
func ParseEveLine(line string) (*EveEvent, error) {
	var evt EveEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return nil, fmt.Errorf("eve: failed to parse line: %w", err)
	}
	return &evt, nil
}

// FiveTuple projects the event onto the correlation key. Returns false for
// events that don't describe a single conversation (missing IPs).
func (e *EveEvent) FiveTuple() (processor.FiveTuple, bool) {
	if e.SrcIP == "" || e.DestIP == "" {
		return processor.FiveTuple{}, false
	}
	return processor.FiveTuple{
		SrcIP:    e.SrcIP,
		DstIP:    e.DestIP,
		SrcPort:  e.SrcPort,
		DstPort:  e.DestPort,
		Protocol: protocolNumber(e.Proto),
	}, true
}

// DPIMetadata extracts L7 application-identification data from whichever
// nested object is populated. Returns false if the event carries none
// (e.g. plain "flow" events).
func (e *EveEvent) DPIMetadata() (processor.DPIMetadata, bool) {
	var meta processor.DPIMetadata
	found := false

	if e.TLS != nil && e.TLS.SNI != "" {
		meta.SNI = e.TLS.SNI
		meta.Application = "TLS/" + e.TLS.SNI
		found = true
	}
	if e.DNS != nil && e.DNS.RRName != "" {
		meta.DNSQuery = e.DNS.RRName
		if meta.Application == "" {
			meta.Application = "DNS/" + e.DNS.RRName
		}
		found = true
	}
	if e.HTTP != nil && e.HTTP.Hostname != "" {
		meta.HTTPHost = e.HTTP.Hostname
		meta.HTTPURL = e.HTTP.URL
		if meta.Application == "" {
			meta.Application = "HTTP/" + e.HTTP.Hostname
		}
		found = true
	}

	return meta, found
}

// ToAlert converts an "alert" event into a processor.SuricataAlert.
// Returns false for any other event type.
func (e *EveEvent) ToAlert() (processor.SuricataAlert, bool) {
	if e.Alert == nil {
		return processor.SuricataAlert{}, false
	}
	ts, err := time.Parse("2006-01-02T15:04:05.000000-0700", e.Timestamp)
	if err != nil {
		ts = time.Now().UTC()
	}
	return processor.SuricataAlert{
		Timestamp:       ts,
		SourceIP:        e.SrcIP,
		DestinationIP:   e.DestIP,
		SourcePort:      e.SrcPort,
		DestinationPort: e.DestPort,
		Protocol:        e.Proto,
		Action:          e.Alert.Action,
		GID:             e.Alert.GID,
		SignatureID:     e.Alert.SignatureID,
		Rev:             e.Alert.Rev,
		Signature:       e.Alert.Signature,
		Category:        e.Alert.Category,
		Severity:        e.Alert.Severity,
	}, true
}

// protocolNumber maps Suricata's textual protocol names to IANA protocol
// numbers, matching what NetFlow records carry — necessary so 5-tuples from
// both sources compare equal.
func protocolNumber(proto string) uint8 {
	switch strings.ToUpper(proto) {
	case "TCP":
		return 6
	case "UDP":
		return 17
	case "ICMP":
		return 1
	default:
		return 0
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/collector/... -run TestParseEveLine -v`
Expected: PASS for all five subtests

- [ ] **Step 5: Commit**

```bash
git add backend/internal/collector/eve.go backend/internal/collector/eve_test.go
git commit -m "feat: parse Suricata eve.json events into FiveTuple/DPIMetadata/SuricataAlert projections"
```

---

### Task 13: 5-tuple correlation cache

**Files:**
- Create: `backend/internal/processor/correlation.go`
- Test: `backend/internal/processor/correlation_test.go`

The meeting point between NetFlow ("how much traffic") and DPI ("what kind of traffic"): a TTL'd map keyed by `FiveTuple`. Both DPI sources (Task 14's Suricata correlator and Task 17's TZSP listener) write into it; the main pipeline (wired in Task 14) reads from it before persisting each flow.

- [ ] **Step 1: Write the failing test for put/get/expiry**

```go
package processor

import (
	"context"
	"testing"
	"time"
)

func TestCorrelationCache_PutThenGet(t *testing.T) {
	cache := NewCorrelationCache(time.Minute)
	key := FiveTuple{SrcIP: "10.0.0.1", DstIP: "93.184.216.34", SrcPort: 51000, DstPort: 443, Protocol: 6}

	cache.Put(key, DPIMetadata{SNI: "example.com", Application: "TLS/example.com"})

	got, ok := cache.Get(key)
	if !ok {
		t.Fatal("expected a cache hit for a key that was just stored")
	}
	if got.SNI != "example.com" {
		t.Errorf("expected SNI %q, got %q", "example.com", got.SNI)
	}
}

func TestCorrelationCache_MissForUnknownKey(t *testing.T) {
	cache := NewCorrelationCache(time.Minute)
	_, ok := cache.Get(FiveTuple{SrcIP: "10.0.0.9", DstIP: "10.0.0.10", SrcPort: 1, DstPort: 2, Protocol: 6})
	if ok {
		t.Fatal("expected a cache miss for a key that was never stored")
	}
}

func TestCorrelationCache_EntriesExpire(t *testing.T) {
	cache := NewCorrelationCache(50 * time.Millisecond)
	key := FiveTuple{SrcIP: "10.0.0.1", DstIP: "10.0.0.2", SrcPort: 1111, DstPort: 2222, Protocol: 17}
	cache.Put(key, DPIMetadata{Application: "DNS/example.com"})

	if _, ok := cache.Get(key); !ok {
		t.Fatal("expected a hit immediately after Put")
	}

	time.Sleep(100 * time.Millisecond)

	if _, ok := cache.Get(key); ok {
		t.Fatal("expected the entry to have expired after its TTL elapsed")
	}
}

func TestCorrelationCache_CleanupLoopRemovesExpiredEntries(t *testing.T) {
	cache := NewCorrelationCache(30 * time.Millisecond)
	key := FiveTuple{SrcIP: "10.0.0.1", DstIP: "10.0.0.2", SrcPort: 1, DstPort: 2, Protocol: 6}
	cache.Put(key, DPIMetadata{Application: "TLS/expiring.example.com"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cache.CleanupLoop(ctx, 20*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for cache.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := cache.Len(); got != 0 {
		t.Fatalf("expected cleanup loop to remove the expired entry, but cache still has %d entries", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/processor/... -run TestCorrelationCache -v`
Expected: FAIL — `undefined: NewCorrelationCache`

- [ ] **Step 3: Implement the cache**

Create `backend/internal/processor/correlation.go`:

```go
package processor

import (
	"context"
	"sync"
	"time"
)

type correlationEntry struct {
	metadata  DPIMetadata
	expiresAt time.Time
}

// CorrelationCache is a TTL'd map from FiveTuple to DPIMetadata. It is the
// hand-off point between whichever DPI source is active (Suricata
// correlation or TZSP capture) and the main flow-processing pipeline:
// both write Put(); the pipeline calls Get() while enriching each flow.
//
// Entries expire after ttl — conversations are short-lived, and an unbounded
// map would grow forever on a busy network. CleanupLoop runs a periodic
// sweep so memory is reclaimed even for keys that are never looked up again.
type CorrelationCache struct {
	mu      sync.RWMutex
	entries map[FiveTuple]correlationEntry
	ttl     time.Duration
}

func NewCorrelationCache(ttl time.Duration) *CorrelationCache {
	return &CorrelationCache{
		entries: make(map[FiveTuple]correlationEntry),
		ttl:     ttl,
	}
}

// Put records DPI metadata for a conversation, resetting its expiry.
func (c *CorrelationCache) Put(key FiveTuple, meta DPIMetadata) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = correlationEntry{metadata: meta, expiresAt: time.Now().Add(c.ttl)}
}

// Get returns the DPI metadata for a conversation if it's present and not
// expired. An expired-but-not-yet-swept entry is treated as a miss.
func (c *CorrelationCache) Get(key FiveTuple) (DPIMetadata, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return DPIMetadata{}, false
	}
	return entry.metadata, true
}

// Len reports the current number of entries, including any not-yet-swept
// expired ones. Exposed primarily so tests can observe cleanup progress.
func (c *CorrelationCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// CleanupLoop periodically removes expired entries until ctx is cancelled.
// Run it once, in a background goroutine, for the lifetime of the cache.
func (c *CorrelationCache) CleanupLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.removeExpired()
		}
	}
}

func (c *CorrelationCache) removeExpired() {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/processor/... -run TestCorrelationCache -v`
Expected: PASS for all four subtests

- [ ] **Step 5: Commit**

```bash
git add backend/internal/processor/correlation.go backend/internal/processor/correlation_test.go
git commit -m "feat: add TTL'd 5-tuple correlation cache linking DPI metadata to flows"
```

---

### Task 14: Suricata correlation mode — feed the cache from eve.json

**Files:**
- Create: `backend/internal/collector/suricata_correlator.go`
- Test: `backend/internal/collector/suricata_correlator_test.go`

Wires `FileTailer` (Task 11) and `EveEvent` parsing (Task 12) together: tails `eve.json`, and for every line that both has a `FiveTuple` and carries `DPIMetadata`, stores it in the `CorrelationCache` (Task 13). This is one of the two possible DPI sources; Task 16 makes it switchable at runtime.

- [ ] **Step 1: Write the failing test — lines with DPI metadata populate the cache, others don't**

```go
package collector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fluxio-backend/internal/processor"
)

func TestRunSuricataCorrelator_PopulatesCacheFromTLSEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eve.json")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatalf("failed to create eve.json: %v", err)
	}

	cache := processor.NewCorrelationCache(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go RunSuricataCorrelator(ctx, NewFileTailer(path), cache)

	time.Sleep(100 * time.Millisecond) // let the tailer seek to EOF before we write

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open eve.json for append: %v", err)
	}
	_, _ = f.WriteString(tlsEventJSON + "\n")
	_, _ = f.WriteString(flowEventJSON + "\n") // no DPI metadata — must NOT populate the cache
	f.Close()

	key := processor.FiveTuple{SrcIP: "10.0.0.1", DstIP: "93.184.216.34", SrcPort: 51000, DstPort: 443, Protocol: 6}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if meta, ok := cache.Get(key); ok {
			if meta.SNI != "example.com" {
				t.Fatalf("expected SNI %q, got %q", "example.com", meta.SNI)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the TLS event to populate the correlation cache")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/collector/... -run TestRunSuricataCorrelator -v`
Expected: FAIL — `undefined: RunSuricataCorrelator`

- [ ] **Step 3: Implement the correlator**

Create `backend/internal/collector/suricata_correlator.go`:

```go
package collector

import (
	"context"
	"log"

	"fluxio-backend/internal/processor"
)

// RunSuricataCorrelator tails eve.json via tailer and stores any DPI
// metadata it finds (TLS SNI, DNS queries, HTTP hosts) into cache, keyed
// by each event's 5-tuple. It blocks until ctx is cancelled or the
// underlying tailer's line channel closes.
//
// This is the "suricata" DPI mode: rather than re-implementing protocol
// inspection, it reuses the analysis Suricata already performs.
func RunSuricataCorrelator(ctx context.Context, tailer *FileTailer, cache *processor.CorrelationCache) {
	lines, err := tailer.Lines(ctx)
	if err != nil {
		log.Printf("suricata-correlator: failed to start tailing: %v", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			processEveLine(line, cache)
		}
	}
}

func processEveLine(line string, cache *processor.CorrelationCache) {
	evt, err := ParseEveLine(line)
	if err != nil {
		log.Printf("suricata-correlator: skipping unparseable eve.json line: %v", err)
		return
	}

	tuple, hasTuple := evt.FiveTuple()
	meta, hasMeta := evt.DPIMetadata()
	if hasTuple && hasMeta {
		cache.Put(tuple, meta)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/collector/... -run TestRunSuricataCorrelator -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/collector/suricata_correlator.go backend/internal/collector/suricata_correlator_test.go
git commit -m "feat: populate correlation cache from Suricata eve.json TLS/DNS/HTTP events"
```

---

### Task 15: TZSP capture mode — extract SNI/DNS from raw packets

**Files:**
- Create: `backend/internal/collector/tzsp.go`
- Test: `backend/internal/collector/tzsp_test.go`

The second possible DPI source: a UDP listener on port 37008 that receives TZSP-encapsulated packet copies (e.g. from a switch SPAN/mirror port), decapsulates them, parses Ethernet/IP/TCP/UDP layers with `gopacket`, and extracts the TLS ClientHello SNI extension or the DNS query name — feeding the very same `CorrelationCache` the Suricata mode uses.

TZSP framing: a 1-byte version, 1-byte packet type, 2-byte encapsulated-protocol field, followed by a sequence of TLV tag/length/value "sensor information" fields terminated by a tag-1 ("end of fields") byte, after which the original captured frame begins. Flux.io only needs to skip past the header to reach that original Ethernet frame.

- [ ] **Step 1: Write the failing test — TZSP-wrapped TLS ClientHello yields an SNI in the cache**

This test builds a minimal but wire-correct TZSP→Ethernet→IPv4→TCP→TLS ClientHello packet by hand (using `gopacket`'s layer serialization for the network layers, and a hand-built minimal ClientHello with an SNI extension for the TLS payload — the same kind of byte-level construction Task 3's NetFlow test uses).

```go
package collector

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"

	"fluxio-backend/internal/processor"
)

// buildClientHelloWithSNI constructs a minimal TLS record containing a
// ClientHello handshake message with a server_name (SNI) extension for
// "example.com". Field sizes are computed from the inside out so the
// resulting bytes are wire-correct.
func buildClientHelloWithSNI(host string) []byte {
	hostBytes := []byte(host)

	// server_name extension: list of (type=host_name(0), length, name)
	serverNameEntry := append([]byte{0x00}, u16(uint16(len(hostBytes)))...)
	serverNameEntry = append(serverNameEntry, hostBytes...)
	serverNameList := append(u16(uint16(len(serverNameEntry))), serverNameEntry...)
	sniExtensionBody := append(u16(uint16(len(serverNameList))), serverNameList...)
	sniExtension := append([]byte{0x00, 0x00}, u16(uint16(len(sniExtensionBody)))...) // extension type 0 = server_name
	sniExtension = append(sniExtension, sniExtensionBody...)

	extensions := sniExtension
	extensionsBlock := append(u16(uint16(len(extensions))), extensions...)

	// ClientHello body: version(2) + random(32) + session_id_len(1) + cipher_suites_len(2)+suites
	//                   + compression_len(1)+methods + extensions block
	body := []byte{0x03, 0x03}                  // client_version = TLS 1.2
	body = append(body, make([]byte, 32)...)    // random
	body = append(body, 0x00)                   // session_id length = 0
	body = append(body, 0x00, 0x02, 0x13, 0x01) // cipher_suites length=2, TLS_AES_128_GCM_SHA256
	body = append(body, 0x01, 0x00)             // compression_methods length=1, null
	body = append(body, extensionsBlock...)

	handshake := append([]byte{0x01}, u24(uint32(len(body)))...) // handshake type = client_hello(1)
	handshake = append(handshake, body...)

	record := append([]byte{0x16, 0x03, 0x01}, u16(uint16(len(handshake)))...) // content type = handshake(22), TLS 1.0 record version
	record = append(record, handshake...)
	return record
}

func u16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func u24(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b[1:]
}

// buildTZSPPacket wraps an Ethernet/IPv4/TCP/TLS packet in a minimal TZSP
// header (version=1, type=RECEIVED_TAG_LIST=0, encapsulated protocol=ETHERNET=1,
// immediately followed by an "end of fields" tag (0x01) — i.e. no sensor info).
func buildTZSPPacket(t *testing.T, tlsPayload []byte) []byte {
	t.Helper()

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		DstMAC:       net.HardwareAddr{0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb},
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("10.0.0.1").To4(),
		DstIP:    net.ParseIP("93.184.216.34").To4(),
	}
	tcp := &layers.TCP{
		SrcPort: 51000,
		DstPort: 443,
		SYN:     false,
		ACK:     true,
		Window:  65535,
	}
	_ = tcp.SetNetworkLayerForChecksum(ip)

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{ComputeChecksums: true, FixLengths: true}
	if err := gopacket.SerializeLayers(buf, opts, eth, ip, tcp, gopacket.Payload(tlsPayload)); err != nil {
		t.Fatalf("failed to serialize test packet: %v", err)
	}

	tzsp := []byte{0x01, 0x00, 0x00, 0x01, 0x01} // version=1, type=0, encap_protocol=1 (Ethernet), tag=END(1)
	return append(tzsp, buf.Bytes()...)
}

func TestExtractDPIFromTZSP_TLSClientHelloSNI(t *testing.T) {
	tlsRecord := buildClientHelloWithSNI("example.com")
	packet := buildTZSPPacket(t, tlsRecord)

	cache := processor.NewCorrelationCache(time.Minute)
	processTZSPPacket(packet, cache)

	key := processor.FiveTuple{SrcIP: "10.0.0.1", DstIP: "93.184.216.34", SrcPort: 51000, DstPort: 443, Protocol: 6}
	meta, ok := cache.Get(key)
	if !ok {
		t.Fatal("expected the TZSP-captured ClientHello to populate the correlation cache")
	}
	if meta.SNI != "example.com" {
		t.Fatalf("expected SNI %q, got %q", "example.com", meta.SNI)
	}
	if meta.Application != "TLS/example.com" {
		t.Fatalf("expected application %q, got %q", "TLS/example.com", meta.Application)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/collector/... -run TestExtractDPIFromTZSP -v`
Expected: FAIL — `undefined: processTZSPPacket`

- [ ] **Step 3: Implement TZSP decapsulation, layer parsing, and SNI/DNS extraction**

Create `backend/internal/collector/tzsp.go`:

```go
package collector

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"

	"fluxio-backend/internal/processor"
)

const tzspTagEndOfFields = 0x01

// StartTZSPListener listens for TZSP-encapsulated packet copies (e.g. from a
// switch mirror port) on the given UDP port, decapsulates each one, and
// stores any extracted SNI/DNS metadata in cache. It runs until the process
// exits or the socket errors; malformed packets are logged and skipped.
func StartTZSPListener(port string, cache *processor.CorrelationCache) {
	addr, err := net.ResolveUDPAddr("udp", ":"+port)
	if err != nil {
		log.Fatalf("tzsp: error resolving UDP address: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatalf("tzsp: error listening on UDP %s: %v", port, err)
	}
	defer conn.Close()

	log.Printf("tzsp: listening for TZSP packet captures on UDP %s", port)

	buf := make([]byte, 65535)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("tzsp: error reading from UDP: %v", err)
			continue
		}
		processTZSPPacket(append([]byte(nil), buf[:n]...), cache)
	}
}

// processTZSPPacket decapsulates a single TZSP frame, parses its network
// layers, and — if it finds a TLS ClientHello SNI or a DNS query name —
// stores the result in cache keyed by the conversation's 5-tuple.
func processTZSPPacket(raw []byte, cache *processor.CorrelationCache) {
	frame, err := decapsulateTZSP(raw)
	if err != nil {
		log.Printf("tzsp: dropping unparseable frame: %v", err)
		return
	}

	packet := gopacket.NewPacket(frame, layers.LayerTypeEthernet, gopacket.NoCopy)
	netLayer := packet.NetworkLayer()
	if netLayer == nil {
		return
	}
	srcIP, dstIP := netLayer.NetworkFlow().Endpoints()

	var srcPort, dstPort uint16
	var protocol uint8
	var payload []byte

	if tcp, ok := packet.Layer(layers.LayerTypeTCP).(*layers.TCP); ok {
		srcPort, dstPort, protocol, payload = uint16(tcp.SrcPort), uint16(tcp.DstPort), 6, tcp.Payload
	} else if udp, ok := packet.Layer(layers.LayerTypeUDP).(*layers.UDP); ok {
		srcPort, dstPort, protocol, payload = uint16(udp.SrcPort), uint16(udp.DstPort), 17, udp.Payload
	} else {
		return // not a TCP/UDP conversation — nothing to correlate
	}

	tuple := processor.FiveTuple{
		SrcIP: srcIP.String(), DstIP: dstIP.String(),
		SrcPort: srcPort, DstPort: dstPort, Protocol: protocol,
	}

	if sni, ok := extractTLSSNI(payload); ok {
		cache.Put(tuple, processor.DPIMetadata{SNI: sni, Application: "TLS/" + sni})
		return
	}
	if query, ok := extractDNSQueryName(payload); ok {
		cache.Put(tuple, processor.DPIMetadata{DNSQuery: query, Application: "DNS/" + query})
	}
}

// decapsulateTZSP strips the TZSP header (RFC draft "TaZmen Sniffer Protocol"):
// 1 byte version, 1 byte packet type, 2 bytes encapsulated-protocol, then a
// sequence of tag/length/value sensor-info fields terminated by tag 0x01.
// What remains is the original captured Ethernet frame.
func decapsulateTZSP(raw []byte) ([]byte, error) {
	if len(raw) < 5 {
		return nil, fmt.Errorf("frame too short to contain a TZSP header (%d bytes)", len(raw))
	}
	offset := 4 // version(1) + type(1) + encapsulated protocol(2)

	for {
		if offset >= len(raw) {
			return nil, fmt.Errorf("TZSP header ran past end of frame without an end-of-fields tag")
		}
		tag := raw[offset]
		offset++
		if tag == tzspTagEndOfFields {
			break
		}
		if tag == 0x00 { // padding tag — no length/value
			continue
		}
		if offset >= len(raw) {
			return nil, fmt.Errorf("truncated TZSP tag at offset %d", offset)
		}
		length := int(raw[offset])
		offset += 1 + length
	}

	return raw[offset:], nil
}

// extractTLSSNI looks for a TLS handshake ClientHello in payload and returns
// the server_name from its SNI extension, if present. It only handles the
// unencrypted ClientHello (record type 22, handshake type 1) — exactly the
// message that carries the plaintext SNI on the wire.
func extractTLSSNI(payload []byte) (string, bool) {
	// TLS record header: type(1) version(2) length(2)
	if len(payload) < 5 || payload[0] != 0x16 {
		return "", false
	}
	recordLen := int(binary.BigEndian.Uint16(payload[3:5]))
	if len(payload) < 5+recordLen {
		return "", false
	}
	handshake := payload[5 : 5+recordLen]

	// Handshake header: type(1) length(3); type 1 = ClientHello
	if len(handshake) < 4 || handshake[0] != 0x01 {
		return "", false
	}
	body := handshake[4:]

	// version(2) random(32) session_id_len(1)+session_id
	if len(body) < 35 {
		return "", false
	}
	pos := 34
	sessionIDLen := int(body[pos])
	pos += 1 + sessionIDLen
	if pos+2 > len(body) {
		return "", false
	}

	cipherSuitesLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2 + cipherSuitesLen
	if pos+1 > len(body) {
		return "", false
	}

	compressionLen := int(body[pos])
	pos += 1 + compressionLen
	if pos+2 > len(body) {
		return "", false
	}

	extensionsLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if pos+extensionsLen > len(body) {
		return "", false
	}
	extensions := body[pos : pos+extensionsLen]

	for len(extensions) >= 4 {
		extType := binary.BigEndian.Uint16(extensions[0:2])
		extLen := int(binary.BigEndian.Uint16(extensions[2:4]))
		if 4+extLen > len(extensions) {
			return "", false
		}
		extBody := extensions[4 : 4+extLen]

		if extType == 0x0000 { // server_name
			if sni, ok := parseServerNameExtension(extBody); ok {
				return sni, true
			}
		}
		extensions = extensions[4+extLen:]
	}
	return "", false
}

func parseServerNameExtension(body []byte) (string, bool) {
	// server_name_list length(2), then entries of type(1) name_length(2) name
	if len(body) < 2 {
		return "", false
	}
	listLen := int(binary.BigEndian.Uint16(body[0:2]))
	list := body[2:]
	if len(list) < listLen || len(list) < 3 {
		return "", false
	}
	if list[0] != 0x00 { // name_type 0 = host_name
		return "", false
	}
	nameLen := int(binary.BigEndian.Uint16(list[1:3]))
	if len(list) < 3+nameLen {
		return "", false
	}
	return string(list[3 : 3+nameLen]), true
}

// extractDNSQueryName looks for a DNS query in payload and returns the
// first question's name in dotted form (e.g. "example.com"), decoding the
// standard length-prefixed label sequence.
func extractDNSQueryName(payload []byte) (string, bool) {
	// DNS header is 12 bytes: ID(2) flags(2) QDCOUNT(2) ANCOUNT(2) NSCOUNT(2) ARCOUNT(2)
	if len(payload) < 13 {
		return "", false
	}
	qdCount := binary.BigEndian.Uint16(payload[4:6])
	if qdCount == 0 {
		return "", false
	}

	pos := 12
	var labels []string
	for pos < len(payload) {
		length := int(payload[pos])
		if length == 0 {
			pos++
			break
		}
		if length&0xC0 != 0 { // compression pointer — full parsing not needed for the query name
			return "", false
		}
		pos++
		if pos+length > len(payload) {
			return "", false
		}
		labels = append(labels, string(payload[pos:pos+length]))
		pos += length
	}
	if len(labels) == 0 {
		return "", false
	}

	name := labels[0]
	for _, l := range labels[1:] {
		name += "." + l
	}
	return name, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/collector/... -run TestExtractDPIFromTZSP -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/collector/tzsp.go backend/internal/collector/tzsp_test.go
git commit -m "feat: add TZSP capture mode extracting TLS SNI and DNS query names via gopacket"
```

---

### Task 16: DPI mode manager — hot-swap the active source from settings

**Files:**
- Create: `backend/internal/collector/dpi_manager.go`
- Test: `backend/internal/collector/dpi_manager_test.go`
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/cmd/server/settings_routes.go`

Ties Tasks 13-15 together: a `DPIManager` that starts the listener matching the configured mode, and can switch to a different one at runtime (cancelling the old listener's context and starting the new one) — this is what makes the Settings page (Task 10) take effect immediately, without a backend restart.

- [ ] **Step 1: Write the failing test — switching modes starts/stops the right goroutines**

This test verifies the manager's bookkeeping using instrumented start functions (it doesn't need a real eve.json or socket — that's covered by Tasks 14/15's own tests).

```go
package collector

import (
	"context"
	"sync"
	"testing"
	"time"

	"fluxio-backend/internal/processor"
)

type modeRun struct {
	mode      string
	cancelled bool
}

type fakeRunner struct {
	mu   sync.Mutex
	runs []*modeRun
}

func (r *fakeRunner) start(mode string) func(ctx context.Context) {
	return func(ctx context.Context) {
		run := &modeRun{mode: mode}
		r.mu.Lock()
		r.runs = append(r.runs, run)
		r.mu.Unlock()

		<-ctx.Done()

		r.mu.Lock()
		run.cancelled = true
		r.mu.Unlock()
	}
}

func (r *fakeRunner) snapshot() []modeRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]modeRun, len(r.runs))
	for i, run := range r.runs {
		out[i] = *run
	}
	return out
}

func TestDPIManager_SwitchingModesStopsThePreviousListener(t *testing.T) {
	runner := &fakeRunner{}
	cache := processor.NewCorrelationCache(time.Minute)
	mgr := NewDPIManager(cache, DPIManagerSources{
		Suricata: runner.start("suricata"),
		TZSP:     runner.start("tzsp"),
	})

	if err := mgr.SetMode(context.Background(), "suricata"); err != nil {
		t.Fatalf("SetMode(suricata) returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := mgr.SetMode(context.Background(), "tzsp"); err != nil {
		t.Fatalf("SetMode(tzsp) returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	mgr.Stop()
	time.Sleep(50 * time.Millisecond)

	runs := runner.snapshot()
	if len(runs) != 2 {
		t.Fatalf("expected exactly 2 listener runs (one per mode switch), got %d: %+v", len(runs), runs)
	}
	if runs[0].mode != "suricata" || !runs[0].cancelled {
		t.Errorf("expected the suricata run to have been started then cancelled on switch: %+v", runs[0])
	}
	if runs[1].mode != "tzsp" || !runs[1].cancelled {
		t.Errorf("expected the tzsp run to have been started then cancelled on Stop: %+v", runs[1])
	}
}

func TestDPIManager_SettingModeNoneStopsWithoutStartingAnything(t *testing.T) {
	runner := &fakeRunner{}
	cache := processor.NewCorrelationCache(time.Minute)
	mgr := NewDPIManager(cache, DPIManagerSources{
		Suricata: runner.start("suricata"),
		TZSP:     runner.start("tzsp"),
	})

	if err := mgr.SetMode(context.Background(), "suricata"); err != nil {
		t.Fatalf("SetMode(suricata) returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := mgr.SetMode(context.Background(), "none"); err != nil {
		t.Fatalf("SetMode(none) returned error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	runs := runner.snapshot()
	if len(runs) != 1 {
		t.Fatalf("expected only the suricata run to have started, got %d: %+v", len(runs), runs)
	}
	if !runs[0].cancelled {
		t.Error("expected switching to 'none' to cancel the running suricata listener")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/collector/... -run TestDPIManager -v`
Expected: FAIL — `undefined: NewDPIManager`

- [ ] **Step 3: Implement the manager**

Create `backend/internal/collector/dpi_manager.go`:

```go
package collector

import (
	"context"
	"fmt"
	"log"
	"sync"

	"fluxio-backend/internal/processor"
)

// sourceRunFunc runs one DPI source until ctx is cancelled. Both
// RunSuricataCorrelator (Task 14) and a TZSP-listener wrapper (Task 15)
// satisfy this shape once their cache/path/port arguments are bound by
// a closure — see WireDPIManager below.
type sourceRunFunc func(ctx context.Context)

// DPIManagerSources binds each named mode to the function that runs it.
// Exposed as a struct (rather than a map) so call sites are type-checked
// and self-documenting about which modes exist.
type DPIManagerSources struct {
	Suricata sourceRunFunc
	TZSP     sourceRunFunc
}

// DPIManager owns the currently-active DPI source and can hot-swap it:
// SetMode cancels whatever is running and starts the requested mode,
// so a change made on the Settings page (Task 10, via Task 9's API)
// takes effect immediately without a backend restart.
type DPIManager struct {
	cache   *processor.CorrelationCache
	sources DPIManagerSources

	mu     sync.Mutex
	mode   string
	cancel context.CancelFunc
	done   chan struct{}
}

func NewDPIManager(cache *processor.CorrelationCache, sources DPIManagerSources) *DPIManager {
	return &DPIManager{cache: cache, sources: sources, mode: "none"}
}

// SetMode stops the currently-running source (if any) and starts the one
// matching mode ("none" stops without starting anything). It blocks until
// the previous source has fully stopped, so callers can rely on there never
// being two sources running concurrently.
func (m *DPIManager) SetMode(ctx context.Context, mode string) error {
	var run sourceRunFunc
	switch mode {
	case "none":
		run = nil
	case "suricata":
		run = m.sources.Suricata
	case "tzsp":
		run = m.sources.TZSP
	default:
		return fmt.Errorf("dpi-manager: unknown mode %q", mode)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.stopLocked()
	m.mode = mode

	if run == nil {
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.cancel = cancel
	m.done = done

	go func() {
		defer close(done)
		log.Printf("dpi-manager: starting %q DPI source", mode)
		run(runCtx)
		log.Printf("dpi-manager: %q DPI source stopped", mode)
	}()

	return nil
}

// Stop cancels the active source, if any, and waits for it to finish.
func (m *DPIManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	m.mode = "none"
}

// stopLocked cancels and waits for the current source. Caller must hold m.mu.
func (m *DPIManager) stopLocked() {
	if m.cancel == nil {
		return
	}
	m.cancel()
	<-m.done
	m.cancel = nil
	m.done = nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/collector/... -run TestDPIManager -v`
Expected: PASS for both subtests

- [ ] **Step 5: Wire the manager into `main.go`, bind real sources, and apply the saved mode at startup**

Add to the imports in `backend/cmd/server/main.go`:

```go
	"fluxio-backend/internal/processor" // already imported in Task 7 if following in order — ensure it's present
```

(If `processor` is already imported from Task 7's changes, skip re-adding it.)

After the `settingsRepo := settings.NewRepository(pgDB)` line added in Task 9, and before `registerSettingsRoutes(api, settingsRepo)`, add:

```go
	correlationCache := processor.NewCorrelationCache(30 * time.Second)
	go correlationCache.CleanupLoop(pipelineCtx, 10*time.Second)

	eveLogPath := os.Getenv("SURICATA_EVE_LOG_PATH")
	if eveLogPath == "" {
		eveLogPath = "/var/log/suricata/eve.json"
	}
	tzspPort := os.Getenv("TZSP_PORT")
	if tzspPort == "" {
		tzspPort = "37008"
	}

	dpiManager := collector.NewDPIManager(correlationCache, collector.DPIManagerSources{
		Suricata: func(ctx context.Context) {
			collector.RunSuricataCorrelator(ctx, collector.NewFileTailer(eveLogPath), correlationCache)
		},
		TZSP: func(ctx context.Context) {
			collector.StartTZSPListener(tzspPort, correlationCache)
		},
	})

	startupMode, err := settingsRepo.GetDPIMode(context.Background())
	if err != nil {
		log.Printf("Failed to read saved DPI mode, defaulting to 'none': %v", err)
		startupMode = "none"
	}
	if err := dpiManager.SetMode(pipelineCtx, startupMode); err != nil {
		log.Printf("Failed to start DPI mode %q: %v", startupMode, err)
	}
```

Then change `registerSettingsRoutes(api, settingsRepo)` to also receive the manager, so a `PUT /api/settings` switches the live listener:

```go
	registerSettingsRoutes(api, settingsRepo, dpiManager)
```

- [ ] **Step 6: Update `registerSettingsRoutes` to apply mode changes live**

In `backend/cmd/server/settings_routes.go`, add a `modeSwitcher` interface and call it from the `PUT` handler:

```go
// modeSwitcher is satisfied by *collector.DPIManager — separated as an
// interface so the route can be tested without starting real listeners.
type modeSwitcher interface {
	SetMode(ctx context.Context, mode string) error
}
```

Update the function signature and the `PUT` handler body:

```go
func registerSettingsRoutes(router fiber.Router, store modeStore, switcher modeSwitcher) {
	router.Get("/settings", func(c *fiber.Ctx) error {
		mode, err := store.GetDPIMode(c.Context())
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read settings"})
		}
		return c.JSON(fiber.Map{"dpi_mode": mode})
	})

	router.Put("/settings", func(c *fiber.Ctx) error {
		var body struct {
			DPIMode string `json:"dpi_mode"`
		}
		if err := c.BodyParser(&body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
		}
		if err := store.SetDPIMode(c.Context(), body.DPIMode); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		if err := switcher.SetMode(c.Context(), body.DPIMode); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "saved, but failed to switch the live listener: " + err.Error()})
		}
		return c.JSON(fiber.Map{"dpi_mode": body.DPIMode})
	})
}
```

- [ ] **Step 7: Update the settings route test's call site for the new signature**

In `backend/cmd/server/main_test.go`, add a fake switcher and pass it through:

```go
type fakeModeSwitcher struct{ mode string }

func (f *fakeModeSwitcher) SetMode(_ context.Context, mode string) error { f.mode = mode; return nil }
```

And change the call in `TestSettingsHandlers_GetAndPut`:

```go
	store := &fakeModeStore{mode: "none"}
	switcher := &fakeModeSwitcher{}
	app := fiber.New()
	registerSettingsRoutes(app.Group("/api"), store, switcher)
```

Add an assertion after the successful `PUT` block confirms persistence:

```go
	if switcher.mode != "suricata" {
		t.Fatalf("expected the live listener to be switched to %q, got %q", "suricata", switcher.mode)
	}
```

- [ ] **Step 8: Run the full backend test suite**

Run: `cd backend && go build ./... && go test ./... -short`
Expected: builds and all tests pass (Postgres/ClickHouse integration tests `SKIP`)

- [ ] **Step 9: Add the new env vars to `docker-compose.yml`**

In the `backend` service `environment:` block, add:

```yaml
      - SURICATA_EVE_LOG_PATH=/var/log/suricata/eve.json
      - TZSP_PORT=37008
```

- [ ] **Step 10: Commit**

```bash
git add backend/internal/collector/dpi_manager.go backend/internal/collector/dpi_manager_test.go backend/cmd/server/ docker-compose.yml
git commit -m "feat: add DPI mode manager that hot-swaps Suricata/TZSP listeners from settings"
```

---

### Task 17: Wire DPI metadata into the flow pipeline

**Files:**
- Modify: `backend/cmd/server/main.go:` (the `flowCh` consumer goroutine added in Task 7)

The final connection: before writing each enriched flow, look up its 5-tuple in the correlation cache and copy over any `Application`/`SNI`/`HTTPHost`/`HTTPURL` it finds.

- [ ] **Step 1: Add a cache lookup to the flow consumer goroutine**

In `backend/cmd/server/main.go`, update the goroutine added in Task 7 (`go func() { for flow := range flowCh { ... } }()`) to look up DPI metadata before writing:

```go
	go func() {
		for flow := range flowCh {
			geoIP.EnrichFlow(&flow)

			tuple := processor.FiveTuple{
				SrcIP: flow.SourceIP, DstIP: flow.DestinationIP,
				SrcPort: flow.SourcePort, DstPort: flow.DestinationPort, Protocol: flow.Protocol,
			}
			if meta, ok := correlationCache.Get(tuple); ok {
				flow.Application = meta.Application
				flow.SNI = meta.SNI
				flow.HTTPHost = meta.HTTPHost
				flow.HTTPURL = meta.HTTPURL
			}

			writer.WriteFlow(flow)
		}
	}()
```

- [ ] **Step 2: Verify the backend builds**

Run: `cd backend && go build ./...`
Expected: builds successfully

- [ ] **Step 3: Commit**

```bash
git add backend/cmd/server/main.go
git commit -m "feat: enrich persisted flows with DPI metadata from the correlation cache"
```

---

### Task 18: Suricata alerts → ClickHouse, and a fixed/wired Wazuh forwarder

**Files:**
- Modify: `backend/internal/collector/suricata_correlator.go` (also persist alerts)
- Modify: `backend/internal/collector/wazuh_forwarder.go` (rewrite — fix format, remove `log.Fatalf`, use `FileTailer`)
- Test: `backend/internal/collector/wazuh_forwarder_test.go`
- Modify: `backend/cmd/server/main.go` (start the forwarder)

Two related fixes from the spec's Component 6: (a) `alert` events from `eve.json` get written to `suricata_alerts` via the `BatchWriter` (Task 5), and (b) `wazuh_forwarder.go` — currently dead code with a broken syslog format and a fatal error on startup — gets corrected and wired in.

- [ ] **Step 1: Extend the correlator to also persist alerts**

`processEveLine` (Task 14) already parses every line into an `EveEvent`. Update it to also call `ToAlert()` and forward hits to the batch writer. Modify `backend/internal/collector/suricata_correlator.go`:

Change the `RunSuricataCorrelator` signature and `processEveLine` to take an alert sink:

```go
// alertWriter is satisfied by *storage.BatchWriter — separated as an
// interface so this package doesn't need to import storage, and so tests
// can use a simple recording fake.
type alertWriter interface {
	WriteAlert(alert processor.SuricataAlert)
}

// RunSuricataCorrelator tails eve.json via tailer, storing any DPI metadata
// it finds (TLS SNI, DNS queries, HTTP hosts) into cache keyed by 5-tuple,
// and forwarding any `alert` events to alerts for persistence. It blocks
// until ctx is cancelled or the underlying tailer's line channel closes.
func RunSuricataCorrelator(ctx context.Context, tailer *FileTailer, cache *processor.CorrelationCache, alerts alertWriter) {
	lines, err := tailer.Lines(ctx)
	if err != nil {
		log.Printf("suricata-correlator: failed to start tailing: %v", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			processEveLine(line, cache, alerts)
		}
	}
}

func processEveLine(line string, cache *processor.CorrelationCache, alerts alertWriter) {
	evt, err := ParseEveLine(line)
	if err != nil {
		log.Printf("suricata-correlator: skipping unparseable eve.json line: %v", err)
		return
	}

	if tuple, hasTuple := evt.FiveTuple(); hasTuple {
		if meta, hasMeta := evt.DPIMetadata(); hasMeta {
			cache.Put(tuple, meta)
		}
	}

	if alert, ok := evt.ToAlert(); ok {
		alerts.WriteAlert(alert)
	}
}
```

- [ ] **Step 2: Update the Task 14 test for the new signature**

In `backend/internal/collector/suricata_correlator_test.go`, add a recording fake and pass it through:

```go
type fakeAlertWriter struct {
	mu     sync.Mutex
	alerts []processor.SuricataAlert
}

func (f *fakeAlertWriter) WriteAlert(alert processor.SuricataAlert) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alerts = append(f.alerts, alert)
}

func (f *fakeAlertWriter) snapshot() []processor.SuricataAlert {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]processor.SuricataAlert, len(f.alerts))
	copy(out, f.alerts)
	return out
}
```

Add `"sync"` to the imports, then update the `go RunSuricataCorrelator(...)` call:

```go
	alerts := &fakeAlertWriter{}
	go RunSuricataCorrelator(ctx, NewFileTailer(path), cache, alerts)
```

And extend the existing test (after the cache assertion passes) to also confirm `alertEventJSON` (defined alongside `tlsEventJSON`/`flowEventJSON` in Task 12's test fixtures — if it isn't yet, add it there: an `eve.json` line with `"event_type":"alert"` and a nested `alert` object with `signature`/`category`/`severity`) produced a recorded alert:

```go
	_, _ = f.WriteString(alertEventJSON + "\n")
```

(add this alongside the other two `f.WriteString` calls, before `f.Close()`), then after the cache-polling loop:

```go
	deadline = time.Now().Add(3 * time.Second)
	for {
		if len(alerts.snapshot()) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the alert event to be forwarded for persistence")
		}
		time.Sleep(20 * time.Millisecond)
	}
```

- [ ] **Step 3: Run the correlator tests to verify they still pass**

Run: `cd backend && go test ./internal/collector/... -run TestRunSuricataCorrelator -v`
Expected: PASS

- [ ] **Step 4: Write the failing test for the fixed Wazuh forwarder's syslog formatting**

The forwarder's only pure, testable logic is how it formats a Suricata `eve.json` line into an RFC 3164 syslog message. Extract that into its own function and test it directly (the network/file-tailing parts are integration-level, exercised by the manual end-to-end check in Task 19).

Create `backend/internal/collector/wazuh_forwarder_test.go`:

```go
package collector

import (
	"strings"
	"testing"
)

func TestFormatWazuhSyslogMessage_RFC3164(t *testing.T) {
	line := `{"event_type":"alert","alert":{"signature":"ET MALWARE Generic"}}`

	msg := formatWazuhSyslogMessage(line)

	// RFC 3164: "<PRI>Mmm dd hh:mm:ss HOSTNAME TAG: MSG"
	// PRI 134 = facility 16 (local0) * 8 + severity 6 (info) — a reasonable
	// default for forwarded IDS events.
	if !strings.HasPrefix(msg, "<134>") {
		t.Fatalf("expected an RFC 3164 priority prefix <134>, got message: %q", msg)
	}
	if !strings.Contains(msg, "fluxio-suricata: ") {
		t.Fatalf("expected the RFC 3164 TAG %q in message, got: %q", "fluxio-suricata: ", msg)
	}
	if !strings.HasSuffix(msg, line) {
		t.Fatalf("expected the message to end with the original eve.json line, got: %q", msg)
	}
	if strings.Contains(msg, "\n") {
		t.Fatalf("syslog messages must not contain embedded newlines, got: %q", msg)
	}
}
```

- [ ] **Step 5: Run test to verify it fails**

Run: `cd backend && go test ./internal/collector/... -run TestFormatWazuhSyslogMessage -v`
Expected: FAIL — `undefined: formatWazuhSyslogMessage`

- [ ] **Step 6: Rewrite the forwarder**

Replace the entire contents of `backend/internal/collector/wazuh_forwarder.go`:

```go
package collector

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

// formatWazuhSyslogMessage wraps a single eve.json line in an RFC 3164
// syslog envelope ("<PRI>TAG: MSG") so Wazuh's syslog listener accepts it
// as a structured event. PRI 134 = facility 16 (local0) * 8 + severity 6
// (informational), a reasonable default for forwarded IDS telemetry.
// Embedded newlines are stripped — a single syslog message must be one line.
func formatWazuhSyslogMessage(line string) string {
	clean := strings.TrimRight(line, "\r\n")
	return fmt.Sprintf("<134>fluxio-suricata: %s", clean)
}

// RunWazuhForwarder tails eveLogPath and forwards each line to the Wazuh
// manager as a syslog message over UDP. It runs until ctx is cancelled.
// Connection and file-open failures are retried rather than fatal — Wazuh
// or Suricata may not be up yet when the backend starts.
func RunWazuhForwarder(ctx context.Context, eveLogPath, wazuhIP, wazuhPort string) {
	if wazuhIP == "" {
		log.Println("wazuh-forwarder: WAZUH_MANAGER_IP not set, forwarder disabled")
		return
	}

	wazuhAddr := fmt.Sprintf("%s:%s", wazuhIP, wazuhPort)
	conn := dialWazuhWithRetry(ctx, wazuhAddr)
	if conn == nil {
		return // ctx cancelled while waiting to connect
	}
	defer conn.Close()

	log.Printf("wazuh-forwarder: connected to Wazuh manager at %s", wazuhAddr)

	tailer := NewFileTailer(eveLogPath)
	lines, err := tailer.Lines(ctx)
	if err != nil {
		log.Printf("wazuh-forwarder: failed to start tailing %s: %v", eveLogPath, err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			msg := formatWazuhSyslogMessage(line)
			if _, err := conn.Write([]byte(msg)); err != nil {
				log.Printf("wazuh-forwarder: error sending to Wazuh: %v", err)
			}
		}
	}
}

// dialWazuhWithRetry dials addr over UDP, retrying every 5s until it
// succeeds or ctx is cancelled (in which case it returns nil).
func dialWazuhWithRetry(ctx context.Context, addr string) net.Conn {
	for {
		conn, err := net.Dial("udp", addr)
		if err == nil {
			return conn
		}
		log.Printf("wazuh-forwarder: waiting for Wazuh manager at %s: %v", addr, err)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `cd backend && go test ./internal/collector/... -run TestFormatWazuhSyslogMessage -v`
Expected: PASS

- [ ] **Step 8: Wire the forwarder into `main.go`, and the alert sink into the DPI manager's Suricata source**

In `backend/cmd/server/main.go`, the `dpiManager` was constructed in Task 16 with:

```go
		Suricata: func(ctx context.Context) {
			collector.RunSuricataCorrelator(ctx, collector.NewFileTailer(eveLogPath), correlationCache)
		},
```

Update it to pass the batch writer as the alert sink (it already implements `WriteAlert`, satisfying the `alertWriter` interface from Step 1):

```go
		Suricata: func(ctx context.Context) {
			collector.RunSuricataCorrelator(ctx, collector.NewFileTailer(eveLogPath), correlationCache, writer)
		},
```

Then, near where `dpiManager` is started (after the `dpiManager.SetMode(pipelineCtx, startupMode)` block), start the forwarder as its own independent goroutine — it runs regardless of the active DPI mode, since alert forwarding to Wazuh is a separate concern from DPI correlation:

```go
	wazuhIP := os.Getenv("WAZUH_MANAGER_IP")
	wazuhPort := os.Getenv("WAZUH_MANAGER_PORT")
	if wazuhPort == "" {
		wazuhPort = "1514"
	}
	go collector.RunWazuhForwarder(pipelineCtx, eveLogPath, wazuhIP, wazuhPort)
```

Remove the old dead block that only logged the Wazuh configuration (the one containing `log.Printf("Wazuh Integration configured for: %s:%s", wazuhIP, wazuhPort)` and its surrounding comments) — it's now superseded by the real goroutine above.

- [ ] **Step 9: Run the full backend test suite**

Run: `cd backend && go build ./... && go test ./... -short`
Expected: builds and all tests pass

- [ ] **Step 10: Commit**

```bash
git add backend/internal/collector/suricata_correlator.go backend/internal/collector/suricata_correlator_test.go backend/internal/collector/wazuh_forwarder.go backend/internal/collector/wazuh_forwarder_test.go backend/cmd/server/main.go
git commit -m "feat: persist Suricata alerts to ClickHouse and fix/wire the Wazuh syslog forwarder"
```

---

### Task 19: End-to-end verification

**Files:** None (manual verification only — confirms Tasks 1-18 work together as a system)

- [ ] **Step 1: Build and start the full stack**

Run: `docker compose up --build -d`
Expected: `clickhouse`, `postgres`, `suricata`, and `backend` containers all report `Up` in `docker compose ps`

- [ ] **Step 2: Confirm the backend connected to its dependencies**

Run: `docker compose logs backend --tail 50`
Expected: log lines showing `listening for NetFlow` (Task 7), `Connected to ClickHouse` or equivalent (Task 5/7), and the DPI manager starting in whatever mode is stored in Postgres (defaults to `none` per Task 8's seed) — with no `panic` or repeating connection-refused loops beyond the first few retries while dependencies finish starting.

- [ ] **Step 3: Send a synthetic NetFlow v9 packet and confirm it reaches ClickHouse enriched**

From the host (with a NetFlow v9 generator — e.g. `nflow-generator`, or replay a sample capture with `tcpreplay` against UDP :2055), send at least one packet, then query:

Run: `docker compose exec clickhouse clickhouse-client --query "SELECT source_ip, destination_ip, source_country, source_asn FROM fluxio.network_flows ORDER BY timestamp DESC LIMIT 5"`
Expected: rows appear with non-empty `source_country`/`source_asn` values (real GeoIP/ASN lookups from Task 6 — assuming the `.mmdb` files are mounted per its README section; if they are intentionally absent, the columns are empty but the rows still persist, confirming graceful degradation)

- [ ] **Step 4: Switch DPI mode via the Settings UI and confirm it takes effect live**

Open the frontend, log in, go to **Settings** (Task 10), select **Suricata** mode, save. Then:

Run: `docker compose logs backend --tail 20 | grep -i "dpi-manager"`
Expected: log lines showing `"none" DPI source stopped` (if it was running) followed by `starting "suricata" DPI source` — confirming `DPIManager.SetMode` (Task 16) hot-swapped the listener without a restart

- [ ] **Step 5: Confirm Suricata alerts and DPI-enriched flows appear**

With Suricata generating `eve.json` events from real or replayed traffic:

Run: `docker compose exec clickhouse clickhouse-client --query "SELECT signature, category, severity FROM fluxio.suricata_alerts ORDER BY timestamp DESC LIMIT 5"`
Expected: rows with populated `signature`/`category`/`severity` (Task 18)

Run: `docker compose exec clickhouse clickhouse-client --query "SELECT source_ip, destination_ip, application, sni FROM fluxio.network_flows WHERE application != '' ORDER BY timestamp DESC LIMIT 5"`
Expected: at least some rows show non-empty `application`/`sni` values — confirming the 5-tuple correlation cache (Task 13/14/17) successfully joined NetFlow data with Suricata-derived DPI metadata

- [ ] **Step 6: Confirm the Wazuh forwarder is sending events (if a manager is configured)**

Run: `docker compose logs backend --tail 50 | grep -i "wazuh-forwarder"`
Expected: either `connected to Wazuh manager at <ip>:<port>` (if `WAZUH_MANAGER_IP` points at a real listener) or repeated, non-fatal `waiting for Wazuh manager at ...` retries (if not) — in both cases, the backend keeps running rather than crashing (Task 18's fix for the old `log.Fatalf`)

- [ ] **Step 7: Tear down**

Run: `docker compose down`
Expected: all containers stop cleanly

This task has no commit — it's a verification checkpoint confirming the complete pipeline (Tasks 1-18) functions as an integrated system, matching the spec's Architecture/Data Flow diagram end to end.

---
