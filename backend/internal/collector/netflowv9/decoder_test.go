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
	rec = appendU32(rec, 1500)                               // IN_BYTES
	rec = appendU32(rec, 10)                                 // IN_PKTS
	rec = append(rec, 6)                                     // PROTOCOL = TCP
	rec = appendU16(rec, 51000)                              // L4_SRC_PORT
	rec = append(rec, net.ParseIP("10.0.0.1").To4()...)      // IPV4_SRC_ADDR
	rec = appendU16(rec, 443)                                // L4_DST_PORT
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

	// The decoder applies a newly-learned template immediately, in the same
	// Decode call — flowsets are processed in the order they appear in the
	// packet, and the template (flowSetID=0) precedes the data (flowSetID=256)
	// in buildPacket's byte layout. So the very first call already decodes
	// the flow record correctly.
	flows, err := d.Decode("192.0.2.1:2055", packet)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow on first decode (template precedes data in the same packet), got %d", len(flows))
	}
	assertFlow(t, flows[0])

	// Sending another packet that contains ONLY a data flowset (no template)
	// should still decode correctly, because the decoder cached the template
	// from the first packet — this is what real exporters rely on: they send
	// templates periodically, then many subsequent packets carry only data
	// flowsets referencing the cached template.
	dataOnlyPacket := buildDataOnlyPacket(t)
	flows, err = d.Decode("192.0.2.1:2055", dataOnlyPacket)
	if err != nil {
		t.Fatalf("second Decode returned error: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow on second decode using the cached template, got %d", len(flows))
	}
	assertFlow(t, flows[0])
}

// assertFlow checks a decoded Flow against the values buildPacket/buildDataOnlyPacket encode.
func assertFlow(t *testing.T, f Flow) {
	t.Helper()
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

// buildDataOnlyPacket assembles a NetFlow v9 packet containing ONLY a Data
// FlowSet for template 256 (no Template FlowSet) — modeling a packet an
// exporter sends after it already sent the template in an earlier packet.
// The record encodes the same field values as buildPacket's, in the same
// 7-field layout (IN_BYTES, IN_PKTS, PROTOCOL, L4_SRC_PORT, IPV4_SRC_ADDR,
// L4_DST_PORT, IPV4_DST_ADDR), so assertFlow can check both packets identically.
func buildDataOnlyPacket(t *testing.T) []byte {
	t.Helper()
	var buf []byte

	buf = appendU16(buf, 9)          // version
	buf = appendU16(buf, 1)          // count
	buf = appendU32(buf, 0)          // sysUptime
	buf = appendU32(buf, 1700000001) // unixSecs
	buf = appendU32(buf, 2)          // sequence
	buf = appendU32(buf, 0)          // sourceID

	var rec []byte
	rec = appendU32(rec, 1500)
	rec = appendU32(rec, 10)
	rec = append(rec, 6)
	rec = appendU16(rec, 51000)
	rec = append(rec, net.ParseIP("10.0.0.1").To4()...)
	rec = appendU16(rec, 443)
	rec = append(rec, net.ParseIP("93.184.216.34").To4()...)

	dataFlowSetLen := 4 + len(rec)
	buf = appendU16(buf, 256)
	buf = appendU16(buf, uint16(dataFlowSetLen))
	buf = append(buf, rec...)

	return buf
}
