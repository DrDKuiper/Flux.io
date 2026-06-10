package collector

import (
	"log"
	"net"
	"time"

	"fluxio-backend/internal/collector/netflowv9"
	"fluxio-backend/internal/processor"
)

// GateFunc reports whether telemetry from exporter addr should be accepted.
// The bool result is "enabled"; the second return mirrors the registry Decision
// shape (it is unused by the listener but keeps call sites self-documenting).
type GateFunc func(addr string) (enabled bool, _ bool)

// applyGate stamps rec.Source with the exporter address and returns whether the
// record should be kept, per the gate decision.
func applyGate(addr string, rec processor.FlowRecord, gate GateFunc) (processor.FlowRecord, bool) {
	enabled, _ := gate(addr)
	if !enabled {
		return rec, false
	}
	rec.Source = addr
	return rec, true
}

// StartNetFlowListener listens for NetFlow v9 / IPFIX UDP packets on the given
// port, decodes them, and pushes normalized FlowRecords onto out. Each record
// is passed through gate: telemetry from a disabled source is dropped, and the
// exporter address is stamped onto kept records. It runs until the process
// exits; malformed packets are logged and skipped so a single bad exporter
// can't take the listener down.
func StartNetFlowListener(port string, out chan<- processor.FlowRecord, gate GateFunc) {
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
		exporter := remoteAddr.IP.String()

		flows, err := decoder.Decode(remoteAddr.String(), buf[:n])
		if err != nil {
			log.Printf("netflow: dropping malformed packet from %v: %v", remoteAddr, err)
			continue
		}

		now := time.Now().UTC()
		for _, flow := range flows {
			rec, keep := applyGate(exporter, toFlowRecord(flow, now), gate)
			if !keep {
				continue
			}
			out <- rec
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
