package processor

import (
	"log"
	"net"

	"github.com/oschwald/geoip2-golang"
)

// GeoIPEnricher resolves country and ASN information for IP addresses using
// local MaxMind GeoLite2 databases. If a database file is missing, the
// corresponding lookups become no-ops (with a one-time startup warning)
// instead of making the service fail to start — useful for local development
// where a developer may not have downloaded the (free, license-gated) databases yet.
type GeoIPEnricher struct {
	cityDB *geoip2.Reader
	asnDB  *geoip2.Reader
}

// NewGeoIPEnricher opens the GeoLite2-City and GeoLite2-ASN databases at the
// given paths. Missing files are logged as warnings, not returned as errors.
func NewGeoIPEnricher(cityDBPath, asnDBPath string) (*GeoIPEnricher, error) {
	e := &GeoIPEnricher{}

	if db, err := geoip2.Open(cityDBPath); err != nil {
		log.Printf("enrichment: GeoLite2-City database unavailable at %q (%v) - country lookups disabled", cityDBPath, err)
	} else if t := db.Metadata().DatabaseType; t != "GeoLite2-City" {
		db.Close()
		log.Printf("enrichment: expected GeoLite2-City database at %q but got %q - country lookups disabled", cityDBPath, t)
	} else {
		e.cityDB = db
	}

	if db, err := geoip2.Open(asnDBPath); err != nil {
		log.Printf("enrichment: GeoLite2-ASN database unavailable at %q (%v) - ASN lookups disabled", asnDBPath, err)
	} else if t := db.Metadata().DatabaseType; t != "GeoLite2-ASN" {
		db.Close()
		log.Printf("enrichment: expected GeoLite2-ASN database at %q but got %q - ASN lookups disabled", asnDBPath, t)
	} else {
		e.asnDB = db
	}

	return e, nil
}

// Close releases the underlying database file handles.
func (e *GeoIPEnricher) Close() {
	if e.cityDB != nil {
		e.cityDB.Close()
	}
	if e.asnDB != nil {
		e.asnDB.Close()
	}
}

// Lookup returns the ISO country code, ASN, and ASN organization for ip.
// Any field whose database isn't loaded, or whose lookup fails (private IPs,
// unknown addresses), is returned as its zero value.
func (e *GeoIPEnricher) Lookup(ipStr string) (country string, asn uint32, asnOrg string) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return "", 0, ""
	}

	if e.cityDB != nil {
		if rec, err := e.cityDB.City(ip); err == nil {
			country = rec.Country.IsoCode
		}
	}

	if e.asnDB != nil {
		if rec, err := e.asnDB.ASN(ip); err == nil {
			asn = uint32(rec.AutonomousSystemNumber)
			asnOrg = rec.AutonomousSystemOrganization
		}
	}

	return country, asn, asnOrg
}

// EnrichFlow adds GeoIP and ASN information to a flow record in place.
func (e *GeoIPEnricher) EnrichFlow(flow *FlowRecord) {
	if flow.SourceIP != "" {
		flow.SourceCountry, flow.SourceASN, flow.SourceASNOrg = e.Lookup(flow.SourceIP)
	}
	if flow.DestinationIP != "" {
		flow.DestCountry, flow.DestASN, flow.DestASNOrg = e.Lookup(flow.DestinationIP)
	}
}
