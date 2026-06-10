package storage

import (
	"context"
	"fmt"
	"time"
)

// srcClause appends "AND source = ?" when source is non-empty, returning the
// clause fragment and the args slice to pass through.
func srcClause(source string) (string, []any) {
	if source == "" {
		return "", nil
	}
	return " AND source = ?", []any{source}
}

func (s *ClickHouseStore) Overview(ctx context.Context, since time.Time, source string) (Overview, error) {
	clause, srcArgs := srcClause(source)
	var o Overview
	args := append([]any{since}, srcArgs...)
	err := s.conn.QueryRow(ctx, `
		SELECT count(), sum(bytes), sum(packets), sum(is_alert)
		FROM network_flows WHERE timestamp >= ?`+clause, args...).
		Scan(&o.Flows, &o.Bytes, &o.Packets, &o.ActiveAlerts)
	if err != nil {
		return Overview{}, fmt.Errorf("clickhouse: overview: %w", err)
	}
	return o, nil
}

func (s *ClickHouseStore) TopTalkers(ctx context.Context, since time.Time, source string, limit int) ([]Talker, error) {
	clause, srcArgs := srcClause(source)
	args := append([]any{since}, srcArgs...)
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, `
		SELECT toString(src_ip), any(src_hostname), sum(bytes), sum(packets), count()
		FROM network_flows WHERE timestamp >= ?`+clause+`
		GROUP BY src_ip ORDER BY sum(bytes) DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: top talkers: %w", err)
	}
	defer rows.Close()
	var out []Talker
	for rows.Next() {
		var t Talker
		if err := rows.Scan(&t.IP, &t.Hostname, &t.Bytes, &t.Packets, &t.Flows); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *ClickHouseStore) TopApps(ctx context.Context, since time.Time, source string, limit int) ([]AppCount, error) {
	clause, srcArgs := srcClause(source)
	args := append([]any{since}, srcArgs...)
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, `
		SELECT application_id, sum(bytes), count()
		FROM network_flows WHERE timestamp >= ? AND application_id != ''`+clause+`
		GROUP BY application_id ORDER BY sum(bytes) DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: top apps: %w", err)
	}
	defer rows.Close()
	var out []AppCount
	for rows.Next() {
		var a AppCount
		if err := rows.Scan(&a.Application, &a.Bytes, &a.Flows); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *ClickHouseStore) Throughput(ctx context.Context, since time.Time, source string, buckets int) ([]ThroughputPoint, error) {
	if buckets <= 0 {
		buckets = 60
	}
	width := time.Since(since) / time.Duration(buckets)
	if width < time.Second {
		width = time.Second
	}
	clause, srcArgs := srcClause(source)
	args := append([]any{int64(width.Seconds()), since}, srcArgs...)
	rows, err := s.conn.Query(ctx, `
		SELECT toStartOfInterval(timestamp, toIntervalSecond(?)) AS bucket, sum(bytes), sum(packets)
		FROM network_flows WHERE timestamp >= ?`+clause+`
		GROUP BY bucket ORDER BY bucket`, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: throughput: %w", err)
	}
	defer rows.Close()
	var out []ThroughputPoint
	for rows.Next() {
		var p ThroughputPoint
		if err := rows.Scan(&p.TS, &p.Bytes, &p.Packets); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *ClickHouseStore) GeoByCountry(ctx context.Context, since time.Time, source string) ([]GeoCount, error) {
	clause, srcArgs := srcClause(source)
	args := append([]any{since}, srcArgs...)
	rows, err := s.conn.Query(ctx, `
		SELECT dst_country, sum(bytes), count()
		FROM network_flows WHERE timestamp >= ? AND dst_country != ''`+clause+`
		GROUP BY dst_country ORDER BY sum(bytes) DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: geo: %w", err)
	}
	defer rows.Close()
	var out []GeoCount
	for rows.Next() {
		var g GeoCount
		if err := rows.Scan(&g.Country, &g.Bytes, &g.Flows); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *ClickHouseStore) FlowsFiltered(ctx context.Context, f FlowFilter) (uint64, []FlowRow, error) {
	where := "timestamp >= ?"
	args := []any{f.Since}
	addEq := func(col, val string) {
		if val != "" {
			where += " AND " + col + " = ?"
			args = append(args, val)
		}
	}
	addEq("source", f.Source)
	addEq("toString(src_ip)", f.SrcIP)
	addEq("toString(dst_ip)", f.DstIP)
	addEq("application_id", f.App)
	addEq("dst_country", f.Country)
	if f.Port != 0 {
		where += " AND (src_port = ? OR dst_port = ?)"
		args = append(args, f.Port, f.Port)
	}

	var total uint64
	if err := s.conn.QueryRow(ctx, `SELECT count() FROM network_flows WHERE `+where, args...).Scan(&total); err != nil {
		return 0, nil, fmt.Errorf("clickhouse: flows count: %w", err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	pageArgs := append(append([]any{}, args...), limit, f.Offset)
	rows, err := s.conn.Query(ctx, `
		SELECT timestamp, source, toString(src_ip), toString(dst_ip), src_port, dst_port, protocol,
		       bytes, packets, application_id, sni, http_host, src_country, dst_country, src_asn_org, dst_asn_org
		FROM network_flows WHERE `+where+`
		ORDER BY timestamp DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return 0, nil, fmt.Errorf("clickhouse: flows page: %w", err)
	}
	defer rows.Close()
	var out []FlowRow
	for rows.Next() {
		var r FlowRow
		if err := rows.Scan(&r.TS, &r.Source, &r.SrcIP, &r.DstIP, &r.SrcPort, &r.DstPort, &r.Protocol,
			&r.Bytes, &r.Packets, &r.Application, &r.SNI, &r.HTTPHost, &r.SrcCountry, &r.DstCountry,
			&r.SrcASNOrg, &r.DstASNOrg); err != nil {
			return 0, nil, err
		}
		out = append(out, r)
	}
	return total, out, rows.Err()
}

func (s *ClickHouseStore) AlertsHistory(ctx context.Context, since time.Time, source string, limit, offset int) (uint64, []AlertRow, error) {
	clause, srcArgs := srcClause(source)
	countArgs := append([]any{since}, srcArgs...)
	var total uint64
	if err := s.conn.QueryRow(ctx, `SELECT count() FROM suricata_alerts WHERE timestamp >= ?`+clause, countArgs...).Scan(&total); err != nil {
		return 0, nil, fmt.Errorf("clickhouse: alerts count: %w", err)
	}
	if limit <= 0 {
		limit = 50
	}
	pageArgs := append(append([]any{}, countArgs...), limit, offset)
	rows, err := s.conn.Query(ctx, `
		SELECT timestamp, source, toString(src_ip), toString(dst_ip), alert_signature, alert_category, alert_severity
		FROM suricata_alerts WHERE timestamp >= ?`+clause+`
		ORDER BY timestamp DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return 0, nil, fmt.Errorf("clickhouse: alerts page: %w", err)
	}
	defer rows.Close()
	var out []AlertRow
	for rows.Next() {
		var a AlertRow
		if err := rows.Scan(&a.TS, &a.Source, &a.SrcIP, &a.DstIP, &a.Signature, &a.Category, &a.Severity); err != nil {
			return 0, nil, err
		}
		out = append(out, a)
	}
	return total, out, rows.Err()
}
