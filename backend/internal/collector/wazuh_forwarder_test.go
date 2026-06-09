package collector

import (
	"strings"
	"testing"
)

func TestFormatWazuhSyslogMessage_RFC3164(t *testing.T) {
	line := `{"event_type":"alert","alert":{"signature":"ET MALWARE Generic"}}`

	msg := formatWazuhSyslogMessage(line)

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
