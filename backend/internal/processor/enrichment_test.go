package processor

import "testing"

func TestGeoIPEnricher_MissingDatabases_NoOpWithoutError(t *testing.T) {
	enricher, err := NewGeoIPEnricher("/nonexistent/City.mmdb", "/nonexistent/ASN.mmdb")
	if err != nil {
		t.Fatalf("expected NewGeoIPEnricher to tolerate missing files, got error: %v", err)
	}

	country, asn, asnOrg := enricher.Lookup("8.8.8.8")
	if country != "" || asn != 0 || asnOrg != "" {
		t.Errorf("expected empty enrichment when databases are absent, got country=%q asn=%d org=%q", country, asn, asnOrg)
	}
}
