CREATE DATABASE IF NOT EXISTS fluxio;

USE fluxio;

CREATE TABLE IF NOT EXISTS network_flows
(
    timestamp DateTime64(3, 'UTC'),
    source String,
    src_ip IPv6,
    dst_ip IPv6,
    src_port UInt16,
    dst_port UInt16,
    protocol UInt8,
    bytes UInt64,
    packets UInt64,
    application_id String,
    sni String,
    http_host String,
    http_url String,
    src_country String,
    dst_country String,
    src_asn UInt32,
    dst_asn UInt32,
    src_asn_org String,
    dst_asn_org String,
    src_hostname String,
    dst_hostname String,
    is_alert UInt8 DEFAULT 0,
    alert_severity UInt8 DEFAULT 0,
    alert_signature String
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(timestamp)
ORDER BY (src_ip, dst_ip, application_id, timestamp)
SETTINGS index_granularity = 8192;

CREATE TABLE IF NOT EXISTS suricata_alerts
(
    timestamp DateTime64(3, 'UTC'),
    source String,
    src_ip IPv6,
    dst_ip IPv6,
    src_port UInt16,
    dst_port UInt16,
    protocol String,
    alert_action String,
    alert_gid UInt32,
    alert_signature_id UInt32,
    alert_rev UInt32,
    alert_signature String,
    alert_category String,
    alert_severity UInt8,
    payload String
)
ENGINE = MergeTree
PARTITION BY toYYYYMMDD(timestamp)
ORDER BY (timestamp, src_ip, dst_ip, alert_signature_id)
SETTINGS index_granularity = 8192;
