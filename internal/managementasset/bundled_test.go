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
	if !bytes.Contains(data, []byte("refresh_page_credentials")) {
		t.Fatal("bundled management panel does not contain refresh-page quota action")
	}
	if !bytes.Contains(data, []byte(`const Ce=he.current==="all"?"all":"page",_e=he.current==="all"?D:Y;_e.length!==0&&E(_e,Ce,$)`)) {
		t.Fatal("bundled management panel does not keep separate page and all refresh scopes")
	}
	if bytes.Contains(data, []byte(`value:"round-robin"`)) {
		t.Fatal("bundled management panel still exposes plain round-robin routing")
	}
	if !bytes.Contains(data, []byte(`value:"quota-round-robin"`)) {
		t.Fatal("bundled management panel does not expose quota-round-robin routing")
	}
	if !bytes.Contains(data, []byte(`value:"reset-time-round-robin"`)) {
		t.Fatal("bundled management panel does not expose reset-time-round-robin routing")
	}
	if !bytes.Contains(data, []byte("quota_sort_label")) || !bytes.Contains(data, []byte("quotaSortOptions")) {
		t.Fatal("bundled management panel does not contain quota sorting controls")
	}
	if !bytes.Contains(data, []byte("quota_sort_reset_soon")) || !bytes.Contains(data, []byte("quotaResetMetric")) {
		t.Fatal("bundled management panel does not contain reset-aware quota sorting controls")
	}
	if bytes.Contains(data, []byte(`Use the top "Refresh all credentials" button`)) {
		t.Fatal("bundled management panel still points idle hint at refresh-all only")
	}
	if !bytes.Contains(data, []byte("Use the top refresh buttons")) {
		t.Fatal("bundled management panel does not mention the refresh buttons in idle hint")
	}
}

func TestBundledManagementHTMLCanBeDisabledByEnv(t *testing.T) {
	t.Setenv("MANAGEMENT_USE_BUNDLED_PANEL", "false")
	if _, ok := BundledManagementHTML(); ok {
		t.Fatal("BundledManagementHTML() ok = true when disabled by env, want false")
	}
}
