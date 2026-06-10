package processor

import "time"

// FlowRecord represents a normalized, enriched network flow,
// mirroring the columns of the ClickHouse `network_flows` table.
type FlowRecord struct {
	Timestamp       time.Time
	Source          string
	SourceIP        string
	DestinationIP   string
	SourcePort      uint16
	DestinationPort uint16
	Protocol        uint8
	Bytes           uint64
	Packets         uint64

	Application string
	SNI         string
	HTTPHost    string
	HTTPURL     string

	SourceCountry string
	DestCountry   string
	SourceASN     uint32
	DestASN       uint32
	SourceASNOrg  string
	DestASNOrg    string

	SourceHostname string
	DestHostname   string

	IsAlert        bool
	AlertSeverity  uint8
	AlertSignature string
}

// FiveTuple uniquely identifies a network conversation and is the
// correlation key between NetFlow records and DPI metadata.
type FiveTuple struct {
	SrcIP    string
	DstIP    string
	SrcPort  uint16
	DstPort  uint16
	Protocol uint8
}

// DPIMetadata is L7 application-identification data extracted by
// whichever DPI source is active (Suricata correlation or TZSP capture).
type DPIMetadata struct {
	Application string
	SNI         string
	DNSQuery    string
	HTTPHost    string
	HTTPURL     string
}

// SuricataAlert mirrors the columns of the ClickHouse `suricata_alerts` table.
type SuricataAlert struct {
	Timestamp       time.Time
	SourceIP        string
	DestinationIP   string
	SourcePort      uint16
	DestinationPort uint16
	Protocol        string
	Action          string
	GID             uint32
	SignatureID     uint32
	Rev             uint32
	Signature       string
	Category        string
	Severity        uint8
	Payload         string
}
