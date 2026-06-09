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
	lines := tailer.Lines(ctx)

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
