package collector

import (
	"log"
	"net"

	"github.com/netsampler/goflow2/v2/decoders/netflow"
	"github.com/netsampler/goflow2/v2/decoders/netflow/netflow9"
	"github.com/netsampler/goflow2/v2/decoders/utils"
)

// StartNetFlowListener starts a UDP listener for NetFlow v9 / IPFIX
func StartNetFlowListener(port string) {
	addr, err := net.ResolveUDPAddr("udp", ":"+port)
	if err != nil {
		log.Fatalf("Error resolving UDP address: %v", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatalf("Error listening on UDP %s: %v", port, err)
	}
	defer conn.Close()

	log.Printf("Listening for NetFlow on UDP %s", port)

	buf := make([]byte, 8192)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("Error reading from UDP: %v", err)
			continue
		}

		// Parse the NetFlow packet (stubbed logic for demonstration)
		// goflow2 provides complex decoders, here we show the entrypoint
		log.Printf("Received %d bytes from %v", n, remoteAddr)
		
		// DPI EXTRACTION STUB
		// Ao processar os templates NetFlow IPFIX ou ler o payload (se for sFlow),
		// procuramos programaticamente assinaturas de L7 (Camada de Aplicação).
		extractDpiMetadata(buf[:n])

		// In a complete implementation, we use format decoders here:
		// e.g. netflow.DecodeMessage(buf[:n])
		_ = netflow.ErrorTemplateNotFound
		_ = netflow9.ErrorTemplateNotFound
		_ = utils.DecodeUNumber([]byte{0}, 1)
	}
}

func extractDpiMetadata(payload []byte) {
	// Lógica de dissecação.
	// Em fluxos não-encriptados na porta 53 (DNS), podemos extrair os QNames.
	// No Client Hello do TLS (Handshake type 1), podemos buscar a extensão SNI.
	// Isso permite inferir se o tráfego é Netflix, WhatsApp, etc.
	log.Println("[DPI] Processing L7 metadata (SNI/DNS) for app categorization...")
}
