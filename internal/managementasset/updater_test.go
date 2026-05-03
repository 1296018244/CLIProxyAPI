package managementasset

import "testing"

func TestManagementPanelAutoUpdateDisabledByEnv(t *testing.T) {
	t.Setenv("MANAGEMENT_DISABLE_AUTO_UPDATE_PANEL", "true")
	if !autoUpdateDisabledByEnv() {
		t.Fatal("autoUpdateDisabledByEnv() = false, want true")
	}
}

func TestManagementPanelAutoUpdateDisabledByEnvAcceptsOneAndYes(t *testing.T) {
	for _, value := range []string{"1", "yes", "on", "TRUE"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MANAGEMENT_DISABLE_AUTO_UPDATE_PANEL", value)
			if !autoUpdateDisabledByEnv() {
				t.Fatalf("autoUpdateDisabledByEnv() = false for %q, want true", value)
			}
		})
	}
}

func TestManagementPanelAutoUpdateDisabledByEnvDefaultFalse(t *testing.T) {
	t.Setenv("MANAGEMENT_DISABLE_AUTO_UPDATE_PANEL", "")
	if autoUpdateDisabledByEnv() {
		t.Fatal("autoUpdateDisabledByEnv() = true, want false")
	}
}
