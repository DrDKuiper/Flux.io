package processor

import (
	"log"
)

// EnrichFlow takes a raw flow record and adds GeoIP and ASN information
func EnrichFlow(flow *FlowRecord) {
	// In a complete implementation, this would query the MaxMind GeoIP2/ASN databases.
	// We stub this for the architectural skeleton.
	
	if flow.SourceIP != "" {
		flow.SourceCountry = lookupCountry(flow.SourceIP)
		flow.SourceASN = lookupASN(flow.SourceIP)
	}
	
	if flow.DestinationIP != "" {
		flow.DestCountry = lookupCountry(flow.DestinationIP)
		flow.DestASN = lookupASN(flow.DestinationIP)
	}
	
	log.Printf("Enriched flow: %s -> %s (App: %s)", flow.SourceIP, flow.DestinationIP, flow.Application)
}

func lookupCountry(ip string) string {
	// Stub
	return "BR"
}

func lookupASN(ip string) uint32 {
	// Stub
	return 28573
}
