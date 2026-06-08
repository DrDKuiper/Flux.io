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
