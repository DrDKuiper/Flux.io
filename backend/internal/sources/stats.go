package sources

import "sync"

// StatSnapshot is the live view for one source.
type StatSnapshot struct {
	FlowsPerSec uint64 // flows counted in the last completed 1s window
	WindowFlows uint64 // flows counted in the in-progress window
	TotalBytes  uint64 // cumulative bytes since process start
	TotalFlows  uint64 // cumulative flows since process start
}

type statCounter struct {
	curFlows  uint64 // flows in the in-progress second
	rateFlows uint64 // flows in the last completed second
	totBytes  uint64
	totFlows  uint64
}

// Stats holds rolling per-source counters. Record is called on the intake hot
// path; Roll is called once per second by a ticker to advance the rate window.
type Stats struct {
	mu sync.Mutex
	by map[string]*statCounter
}

func NewStats() *Stats { return &Stats{by: make(map[string]*statCounter)} }

// Record counts one flow of the given byte size for address.
func (s *Stats) Record(address string, bytes uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.by[address]
	if c == nil {
		c = &statCounter{}
		s.by[address] = c
	}
	c.curFlows++
	c.totFlows++
	c.totBytes += bytes
}

// Roll advances every counter's rate window: the in-progress second becomes the
// reported per-second rate, and a fresh window starts at zero.
func (s *Stats) Roll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.by {
		c.rateFlows = c.curFlows
		c.curFlows = 0
	}
}

// Snapshot returns the live view for address (zero value if unseen).
func (s *Stats) Snapshot(address string) StatSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.by[address]
	if c == nil {
		return StatSnapshot{}
	}
	return StatSnapshot{
		FlowsPerSec: c.rateFlows,
		WindowFlows: c.curFlows,
		TotalBytes:  c.totBytes,
		TotalFlows:  c.totFlows,
	}
}
