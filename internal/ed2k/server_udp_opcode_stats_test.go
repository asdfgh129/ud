package goed2k

import (
	"testing"

	serverproto "github.com/asdfgh129/gobitmule/internal/ed2k/protocol/server"
)

// AnswerOpcodes is recorded on the receive path before body parsing. This is
// what lets evidence distinguish "the server answered with 0x9B but we could
// not parse its body" from "the server never answered at all" — a
// parsed-only counter collapses both into "no source answer".
func TestUDPTrafficStatsAnswerOpcodes(t *testing.T) {
	var s UDPTrafficStats
	s = s.recordAnswerOpcode(serverproto.OpGlobFoundSources)
	s = s.recordAnswerOpcode(serverproto.OpGlobFoundSources)
	s = s.recordAnswerOpcode(serverproto.OpGlobSearchRes)

	if got := s.AnswerOpcodes["0x9b"]; got != 2 {
		t.Fatalf("0x9b count = %d, want 2", got)
	}
	if got := s.AnswerOpcodes["0x99"]; got != 1 {
		t.Fatalf("0x99 count = %d, want 1", got)
	}
	if len(s.AnswerOpcodes) != 2 {
		t.Fatalf("opcode kinds = %d, want 2", len(s.AnswerOpcodes))
	}
}

// TrafficStats returns a copy of the map. Probes take a before/after snapshot
// and subtract counters; if the map were shared the delta would always be
// zero and the opcode breakdown would be meaningless.
func TestServerUDPTrafficStatsReturnsIndependentMap(t *testing.T) {
	u := NewServerUDP()
	u.stats = u.stats.recordAnswerOpcode(serverproto.OpGlobFoundSources)

	snapshot := u.TrafficStats()
	snapshot.AnswerOpcodes["0x9b"] = 999
	after := u.TrafficStats()

	if got := after.AnswerOpcodes["0x9b"]; got != 1 {
		t.Fatalf("mutating the snapshot changed live stats: got %d, want 1", got)
	}
	u.stats = u.stats.recordAnswerOpcode(serverproto.OpGlobFoundSources)
	if got := snapshot.AnswerOpcodes["0x9b"]; got != 999 {
		t.Fatalf("live updates changed the snapshot: got %d, want 999", got)
	}
}
