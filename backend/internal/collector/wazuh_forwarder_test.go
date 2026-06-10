package collector

import (
	"strings"
	"testing"
)

func TestFormatWazuhSyslogMessage_RFC3164(t *testing.T) {
	line := `{"event_type":"alert","alert":{"signature":"ET MALWARE Generic"}}`

	msg := formatWazuhSyslogMessage(line)

	// RFC 3164: PRI 134 = facility 16 (local0) * 8 + severity 6 (informational).
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

func TestFormatWazuhSyslogMessage_StripsTrailingNewlines(t *testing.T) {
	// FileTailer delivers lines with trailing newlines stripped, but syslog
	// messages written directly from other sources may include CR/LF.
	// Verify that TrimRight removes them so the UDP payload stays single-line.
	payload := `{"event_type":"alert","alert":{"signature":"ET SCAN"}}` + "\r\n"

	msg := formatWazuhSyslogMessage(payload)

	if strings.Contains(msg, "\r") || strings.Contains(msg, "\n") {
		t.Fatalf("expected trailing CR/LF to be stripped, got: %q", msg)
	}
	want := `{"event_type":"alert","alert":{"signature":"ET SCAN"}}`
	if !strings.HasSuffix(msg, want) {
		t.Fatalf("expected message to end with stripped payload %q, got: %q", want, msg)
	}
}
