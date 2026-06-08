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
