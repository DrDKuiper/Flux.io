package collector

import (
	"context"
	"encoding/binary"
	"errors"
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
// stores any extracted SNI/DNS metadata in cache. It runs until the socket
// is closed or returns a permanent error, then returns that error.
func StartTZSPListener(ctx context.Context, port string, cache *processor.CorrelationCache) error {
	addr, err := net.ResolveUDPAddr("udp", ":"+port)
	if err != nil {
		return fmt.Errorf("tzsp: resolving UDP address: %w", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("tzsp: listening on UDP %s: %w", port, err)
	}
	defer conn.Close()

	// Close the connection when ctx is cancelled so ReadFromUDP unblocks immediately.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	log.Printf("tzsp: listening for TZSP packet captures on UDP %s", port)

	buf := make([]byte, 65535)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil // context cancelled or normal shutdown
			}
			log.Printf("tzsp: read error: %v", err)
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
		cache.Put(tuple, processor.DPIMetadata{SNI: sni, Application: "TLS/" + sni}, "tzsp")
		return
	}
	if query, ok := extractDNSQueryName(payload); ok {
		cache.Put(tuple, processor.DPIMetadata{DNSQuery: query, Application: "DNS/" + query}, "tzsp")
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
		offset++ // consume the length byte
		if offset+length > len(raw) {
			return nil, fmt.Errorf("TZSP TLV value claims length %d but only %d bytes remain at offset %d",
				length, len(raw)-offset, offset)
		}
		offset += length
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
