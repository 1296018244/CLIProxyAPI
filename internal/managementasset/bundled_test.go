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
	if bytes.Contains(data, []byte("cpa-codex-quota-refresh-all-helper")) {
		t.Fatal("bundled management panel contains the deprecated floating quota refresh helper")
	}
	if !bytes.Contains(data, []byte(`const Ce="all",_e=D;_e.length!==0&&E(_e,Ce,$)`)) {
		t.Fatal("bundled management panel refresh-all quota button does not target all filtered credentials")
	}
}

func TestBundledManagementHTMLCanBeDisabledByEnv(t *testing.T) {
	t.Setenv("MANAGEMENT_USE_BUNDLED_PANEL", "false")
	if _, ok := BundledManagementHTML(); ok {
		t.Fatal("BundledManagementHTML() ok = true when disabled by env, want false")
	}
}
