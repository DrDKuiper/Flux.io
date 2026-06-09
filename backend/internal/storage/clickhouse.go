package storage

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"fluxio-backend/internal/processor"
)

// ClickHouseStore is the production Inserter: it batches records into
// native ClickHouse batch inserts against the schema created by
// db/clickhouse/init-db.sql.
type ClickHouseStore struct {
	conn driver.Conn
}

func NewClickHouseStore(dsn string) (*ClickHouseStore, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: invalid DSN: %w", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: failed to connect: %w", err)
	}
	if err := conn.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("clickhouse: ping failed: %w", err)
	}
	return &ClickHouseStore{conn: conn}, nil
}

func (s *ClickHouseStore) InsertFlows(ctx context.Context, records []processor.FlowRecord) error {
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO network_flows (
		timestamp, src_ip, dst_ip, src_port, dst_port, protocol, bytes, packets,
		application_id, sni, http_host, http_url,
		src_country, dst_country, src_asn, dst_asn, src_asn_org, dst_asn_org,
		src_hostname, dst_hostname, is_alert, alert_severity, alert_signature
	)`)
	if err != nil {
		return fmt.Errorf("clickhouse: prepare flow batch: %w", err)
	}

	for _, r := range records {
		isAlert := uint8(0)
		if r.IsAlert {
			isAlert = 1
		}
		err := batch.Append(
			r.Timestamp, r.SourceIP, r.DestinationIP, r.SourcePort, r.DestinationPort,
			r.Protocol, r.Bytes, r.Packets,
			r.Application, r.SNI, r.HTTPHost, r.HTTPURL,
			r.SourceCountry, r.DestCountry, r.SourceASN, r.DestASN, r.SourceASNOrg, r.DestASNOrg,
			r.SourceHostname, r.DestHostname, isAlert, r.AlertSeverity, r.AlertSignature,
		)
		if err != nil {
			return fmt.Errorf("clickhouse: append flow record: %w", err)
		}
	}
	return batch.Send()
}

func (s *ClickHouseStore) InsertAlerts(ctx context.Context, alerts []processor.SuricataAlert) error {
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO suricata_alerts (
		timestamp, src_ip, dst_ip, src_port, dst_port, protocol,
		alert_action, alert_gid, alert_signature_id, alert_rev,
		alert_signature, alert_category, alert_severity, payload
	)`)
	if err != nil {
		return fmt.Errorf("clickhouse: prepare alert batch: %w", err)
	}

	for _, a := range alerts {
		err := batch.Append(
			a.Timestamp, a.SourceIP, a.DestinationIP, a.SourcePort, a.DestinationPort, a.Protocol,
			a.Action, a.GID, a.SignatureID, a.Rev,
			a.Signature, a.Category, a.Severity, a.Payload,
		)
		if err != nil {
			return fmt.Errorf("clickhouse: append alert record: %w", err)
		}
	}
	return batch.Send()
}
