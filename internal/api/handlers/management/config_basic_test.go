package management

import "testing"

func TestNormalizeRoutingStrategyQuotaRoundRobin(t *testing.T) {
	for _, raw := range []string{"", "round-robin", "roundrobin", "rr", "quota-round-robin", "quota-roundrobin", "quota-rr", "qrr"} {
		got, ok := normalizeRoutingStrategy(raw)
		if !ok {
			t.Fatalf("normalizeRoutingStrategy(%q) ok=false, want true", raw)
		}
		if got != "quota-round-robin" {
			t.Fatalf("normalizeRoutingStrategy(%q) = %q, want quota-round-robin", raw, got)
		}
	}
}

func TestNormalizeRoutingStrategyResetTimeRoundRobin(t *testing.T) {
	for _, raw := range []string{"reset-time-round-robin", "reset-time-roundrobin", "reset-time-rr", "reset-rr", "rtrr"} {
		got, ok := normalizeRoutingStrategy(raw)
		if !ok {
			t.Fatalf("normalizeRoutingStrategy(%q) ok=false, want true", raw)
		}
		if got != "reset-time-round-robin" {
			t.Fatalf("normalizeRoutingStrategy(%q) = %q, want reset-time-round-robin", raw, got)
		}
	}
}
