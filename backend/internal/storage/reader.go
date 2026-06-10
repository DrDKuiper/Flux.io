package storage

import (
	"context"
	"time"
)

// Overview is the dashboard's headline totals.
type Overview struct {
	Flows        uint64 `json:"flows"`
	Bytes        uint64 `json:"bytes"`
	Packets      uint64 `json:"packets"`
	ActiveAlerts uint64 `json:"active_alerts"`
}

type Talker struct {
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	Bytes    uint64 `json:"bytes"`
	Packets  uint64 `json:"packets"`
	Flows    uint64 `json:"flows"`
}

type AppCount struct {
	Application string `json:"application_id"`
	Bytes       uint64 `json:"bytes"`
	Flows       uint64 `json:"flows"`
}

type ThroughputPoint struct {
	TS      time.Time `json:"ts"`
	Bytes   uint64    `json:"bytes"`
	Packets uint64    `json:"packets"`
}

type GeoCount struct {
	Country string `json:"country"`
	Bytes   uint64 `json:"bytes"`
	Flows   uint64 `json:"flows"`
}

type FlowRow struct {
	TS          time.Time `json:"ts"`
	Source      string    `json:"source"`
	SrcIP       string    `json:"src_ip"`
	DstIP       string    `json:"dst_ip"`
	SrcPort     uint16    `json:"src_port"`
	DstPort     uint16    `json:"dst_port"`
	Protocol    uint8     `json:"protocol"`
	Bytes       uint64    `json:"bytes"`
	Packets     uint64    `json:"packets"`
	Application string    `json:"application_id"`
	SNI         string    `json:"sni"`
	HTTPHost    string    `json:"http_host"`
	SrcCountry  string    `json:"src_country"`
	DstCountry  string    `json:"dst_country"`
	SrcASNOrg   string    `json:"src_asn_org"`
	DstASNOrg   string    `json:"dst_asn_org"`
}

type AlertRow struct {
	TS        time.Time `json:"ts"`
	Source    string    `json:"source"`
	SrcIP     string    `json:"src_ip"`
	DstIP     string    `json:"dst_ip"`
	Signature string    `json:"signature"`
	Category  string    `json:"category"`
	Severity  uint8     `json:"severity"`
}

// FlowFilter holds the optional filters for the flow explorer. Zero-valued
// fields mean "no filter on that dimension".
type FlowFilter struct {
	Since   time.Time
	Source  string
	SrcIP   string
	DstIP   string
	App     string
	Country string
	Port    uint16
	Limit   int
	Offset  int
}

// Reader is the read side of the data store, consumed by the api package.
// *ClickHouseStore implements it; api tests use a fake.
type Reader interface {
	Overview(ctx context.Context, since time.Time, source string) (Overview, error)
	TopTalkers(ctx context.Context, since time.Time, source string, limit int) ([]Talker, error)
	TopApps(ctx context.Context, since time.Time, source string, limit int) ([]AppCount, error)
	Throughput(ctx context.Context, since time.Time, source string, buckets int) ([]ThroughputPoint, error)
	GeoByCountry(ctx context.Context, since time.Time, source string) ([]GeoCount, error)
	FlowsFiltered(ctx context.Context, f FlowFilter) (total uint64, items []FlowRow, err error)
	AlertsHistory(ctx context.Context, since time.Time, source string, limit, offset int) (total uint64, items []AlertRow, err error)
}
