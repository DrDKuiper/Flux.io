// Package sources tracks the hosts/sensors that send telemetry to Flux.io.
// Sources are auto-discovered by their origin address; each carries per-host
// configuration (name, group, enabled, DPI mode) and live status.
package sources

import "time"

// Source is one telemetry origin (a NetFlow/TZSP exporter or the Suricata sensor).
type Source struct {
	ID           int       `json:"id"`
	Address      string    `json:"address"`
	Type         string    `json:"type"`
	Name         string    `json:"name"`
	GroupTag     string    `json:"group_tag"`
	Enabled      bool      `json:"enabled"`
	DPIMode      string    `json:"dpi_mode"`
	ExpectedType string    `json:"expected_type"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
}

// Decision is what Observe returns to the intake hot path.
type Decision struct {
	Enabled bool
	DPIMode string
}

var validDPIModes = map[string]bool{"auto": true, "suricata": true, "tzsp": true, "none": true}
var validTypes = map[string]bool{"netflow": true, "tzsp": true, "suricata": true}

// ValidDPIMode reports whether mode is one the system can honor.
func ValidDPIMode(mode string) bool { return validDPIModes[mode] }

// ValidType reports whether ty is a recognized source type.
func ValidType(ty string) bool { return validTypes[ty] }
