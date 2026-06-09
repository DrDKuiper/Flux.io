package collector

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fluxio-backend/internal/processor"
)

// EveEvent is a typed projection of the subset of Suricata eve.json fields
// Flux.io needs. Suricata emits many event_types on the same stream (flow,
// tls, dns, http, alert, ...); the relevant nested object is populated
// depending on event_type and the others are nil.
type EveEvent struct {
	Timestamp string    `json:"timestamp"`
	EventType string    `json:"event_type"`
	SrcIP     string    `json:"src_ip"`
	SrcPort   uint16    `json:"src_port"`
	DestIP    string    `json:"dest_ip"`
	DestPort  uint16    `json:"dest_port"`
	Proto     string    `json:"proto"`
	Alert     *eveAlert `json:"alert,omitempty"`
	TLS       *eveTLS   `json:"tls,omitempty"`
	DNS       *eveDNS   `json:"dns,omitempty"`
	HTTP      *eveHTTP  `json:"http,omitempty"`
}

type eveAlert struct {
	Action      string `json:"action"`
	GID         uint32 `json:"gid"`
	SignatureID uint32 `json:"signature_id"`
	Rev         uint32 `json:"rev"`
	Signature   string `json:"signature"`
	Category    string `json:"category"`
	Severity    uint8  `json:"severity"`
}

type eveTLS struct {
	SNI string `json:"sni"`
}

// eveDNS assumes Suricata's flat eve-log DNS format (eve-log.dns.version: 1),
// where rrname is a single string. The newer nested format (version: 2) uses
// a `query` array instead — documented as a prerequisite in Task 16.
type eveDNS struct {
	RRName string `json:"rrname"`
	RRType string `json:"rrtype"`
}

type eveHTTP struct {
	Hostname string `json:"hostname"`
	URL      string `json:"url"`
}

// ParseEveLine decodes a single line of Suricata's eve.json.
func ParseEveLine(line string) (*EveEvent, error) {
	var evt EveEvent
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return nil, fmt.Errorf("eve: failed to parse line: %w", err)
	}
	return &evt, nil
}

// FiveTuple projects the event onto the correlation key. Returns false for
// events that don't describe a single conversation (missing IPs).
func (e *EveEvent) FiveTuple() (processor.FiveTuple, bool) {
	if e.SrcIP == "" || e.DestIP == "" {
		return processor.FiveTuple{}, false
	}
	return processor.FiveTuple{
		SrcIP:    e.SrcIP,
		DstIP:    e.DestIP,
		SrcPort:  e.SrcPort,
		DstPort:  e.DestPort,
		Protocol: protocolNumber(e.Proto),
	}, true
}

// DPIMetadata extracts L7 application-identification data from whichever
// nested object is populated. Returns false if the event carries none
// (e.g. plain "flow" events).
func (e *EveEvent) DPIMetadata() (processor.DPIMetadata, bool) {
	var meta processor.DPIMetadata
	found := false

	if e.TLS != nil && e.TLS.SNI != "" {
		meta.SNI = e.TLS.SNI
		meta.Application = "TLS/" + e.TLS.SNI
		found = true
	}
	if e.DNS != nil && e.DNS.RRName != "" {
		meta.DNSQuery = e.DNS.RRName
		if meta.Application == "" {
			meta.Application = "DNS/" + e.DNS.RRName
		}
		found = true
	}
	if e.HTTP != nil && e.HTTP.Hostname != "" {
		meta.HTTPHost = e.HTTP.Hostname
		meta.HTTPURL = e.HTTP.URL
		if meta.Application == "" {
			meta.Application = "HTTP/" + e.HTTP.Hostname
		}
		found = true
	}

	return meta, found
}

// ToAlert converts an "alert" event into a processor.SuricataAlert.
// Returns false for any other event type.
func (e *EveEvent) ToAlert() (processor.SuricataAlert, bool) {
	if e.Alert == nil {
		return processor.SuricataAlert{}, false
	}
	ts, err := time.Parse("2006-01-02T15:04:05.000000-0700", e.Timestamp)
	if err != nil {
		ts = time.Now().UTC()
	}
	return processor.SuricataAlert{
		Timestamp:       ts,
		SourceIP:        e.SrcIP,
		DestinationIP:   e.DestIP,
		SourcePort:      e.SrcPort,
		DestinationPort: e.DestPort,
		Protocol:        e.Proto,
		Action:          e.Alert.Action,
		GID:             e.Alert.GID,
		SignatureID:     e.Alert.SignatureID,
		Rev:             e.Alert.Rev,
		Signature:       e.Alert.Signature,
		Category:        e.Alert.Category,
		Severity:        e.Alert.Severity,
	}, true
}

// protocolNumber maps Suricata's textual protocol names to IANA protocol
// numbers, matching what NetFlow records carry — necessary so 5-tuples from
// both sources compare equal.
func protocolNumber(proto string) uint8 {
	switch strings.ToUpper(proto) {
	case "TCP":
		return 6
	case "UDP":
		return 17
	case "ICMP":
		return 1
	default:
		return 0
	}
}
