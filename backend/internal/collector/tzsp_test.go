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
// the given host. Field sizes are computed from the inside out so the
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

// u24 encodes v as a 3-byte big-endian integer. Precondition: v <= 0xFFFFFF
// (values > 16 MiB are silently truncated, but TLS handshake bodies are
// always far smaller than this limit).
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
