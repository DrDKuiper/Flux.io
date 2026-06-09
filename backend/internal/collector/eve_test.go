package collector

import "testing"

const tlsEventJSON = `{"timestamp":"2026-06-08T12:00:00.000000+0000","event_type":"tls","src_ip":"10.0.0.1","src_port":51000,"dest_ip":"93.184.216.34","dest_port":443,"proto":"TCP","tls":{"sni":"example.com"}}`

const dnsEventJSON = `{"timestamp":"2026-06-08T12:00:01.000000+0000","event_type":"dns","src_ip":"10.0.0.1","src_port":53124,"dest_ip":"8.8.8.8","dest_port":53,"proto":"UDP","dns":{"rrname":"example.com","rrtype":"A"}}`

const httpEventJSON = `{"timestamp":"2026-06-08T12:00:02.000000+0000","event_type":"http","src_ip":"10.0.0.1","src_port":51010,"dest_ip":"93.184.216.34","dest_port":80,"proto":"TCP","http":{"hostname":"example.com","url":"/index.html"}}`

const alertEventJSON = `{"timestamp":"2026-06-08T12:00:03.000000+0000","event_type":"alert","src_ip":"203.0.113.9","src_port":443,"dest_ip":"10.0.0.5","dest_port":51022,"proto":"TCP","alert":{"action":"allowed","gid":1,"signature_id":2024897,"rev":2,"signature":"ET MALWARE Possible C2 Beacon","category":"A Network Trojan was Detected","severity":1}}`

const flowEventJSON = `{"timestamp":"2026-06-08T12:00:04.000000+0000","event_type":"flow","src_ip":"10.0.0.1","src_port":51000,"dest_ip":"93.184.216.34","dest_port":443,"proto":"TCP"}`

func TestParseEveLine_TLS(t *testing.T) {
	evt, err := ParseEveLine(tlsEventJSON)
	if err != nil {
		t.Fatalf("ParseEveLine returned error: %v", err)
	}

	tuple, ok := evt.FiveTuple()
	if !ok {
		t.Fatal("expected a valid FiveTuple from a TLS event")
	}
	if tuple.SrcIP != "10.0.0.1" || tuple.DstIP != "93.184.216.34" || tuple.SrcPort != 51000 || tuple.DstPort != 443 || tuple.Protocol != 6 {
		t.Errorf("unexpected tuple: %+v", tuple)
	}

	meta, ok := evt.DPIMetadata()
	if !ok {
		t.Fatal("expected DPI metadata from a TLS event with SNI")
	}
	if meta.SNI != "example.com" {
		t.Errorf("expected SNI %q, got %q", "example.com", meta.SNI)
	}
	if meta.Application != "TLS/example.com" {
		t.Errorf("expected application %q, got %q", "TLS/example.com", meta.Application)
	}
}

func TestParseEveLine_DNS(t *testing.T) {
	evt, err := ParseEveLine(dnsEventJSON)
	if err != nil {
		t.Fatalf("ParseEveLine returned error: %v", err)
	}
	meta, ok := evt.DPIMetadata()
	if !ok || meta.DNSQuery != "example.com" {
		t.Fatalf("expected DNS query %q, got ok=%v meta=%+v", "example.com", ok, meta)
	}
}

func TestParseEveLine_HTTP(t *testing.T) {
	evt, err := ParseEveLine(httpEventJSON)
	if err != nil {
		t.Fatalf("ParseEveLine returned error: %v", err)
	}
	meta, ok := evt.DPIMetadata()
	if !ok || meta.HTTPHost != "example.com" || meta.HTTPURL != "/index.html" {
		t.Fatalf("expected HTTP host/url, got ok=%v meta=%+v", ok, meta)
	}
}

func TestParseEveLine_FlowHasNoMetadata(t *testing.T) {
	evt, err := ParseEveLine(flowEventJSON)
	if err != nil {
		t.Fatalf("ParseEveLine returned error: %v", err)
	}
	if _, ok := evt.DPIMetadata(); ok {
		t.Error("expected a plain 'flow' event to carry no DPI metadata")
	}
	if _, ok := evt.ToAlert(); ok {
		t.Error("expected a plain 'flow' event to not be an alert")
	}
}

func TestParseEveLine_Alert(t *testing.T) {
	evt, err := ParseEveLine(alertEventJSON)
	if err != nil {
		t.Fatalf("ParseEveLine returned error: %v", err)
	}
	alert, ok := evt.ToAlert()
	if !ok {
		t.Fatal("expected a valid alert from an 'alert' event")
	}
	if alert.SourceIP != "203.0.113.9" || alert.SignatureID != 2024897 || alert.Severity != 1 {
		t.Errorf("unexpected alert fields: %+v", alert)
	}
	if alert.Signature != "ET MALWARE Possible C2 Beacon" {
		t.Errorf("unexpected signature: %q", alert.Signature)
	}
}
