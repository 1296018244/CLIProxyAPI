package managementasset

import (
	"bytes"
	"testing"
)

func TestBundledManagementHTMLContainsUsageStatsPanel(t *testing.T) {
	t.Setenv("MANAGEMENT_USE_BUNDLED_PANEL", "")
	data, ok := BundledManagementHTML()
	if !ok {
		t.Fatal("BundledManagementHTML() ok = false, want true")
	}
	if !bytes.Contains(data, []byte("/usage/export")) {
		t.Fatal("bundled management panel does not contain usage export endpoint")
	}
	if !bytes.Contains(data, []byte("/codex-quota/refresh-all")) {
		t.Fatal("bundled management panel does not contain codex quota refresh-all endpoint")
	}
	if !bytes.Contains(data, []byte("TEXT_REFRESH")) {
		t.Fatal("bundled management panel does not contain refresh-all quota button")
	}
}

func TestBundledManagementHTMLCanBeDisabledByEnv(t *testing.T) {
	t.Setenv("MANAGEMENT_USE_BUNDLED_PANEL", "false")
	if _, ok := BundledManagementHTML(); ok {
		t.Fatal("BundledManagementHTML() ok = true when disabled by env, want false")
	}
}
