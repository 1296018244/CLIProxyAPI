package managementasset

import (
	_ "embed"
	"os"
	"strings"
)

//go:embed management.html
var bundledManagementHTML []byte

// BundledManagementHTML returns the management panel embedded in the binary.
func BundledManagementHTML() ([]byte, bool) {
	if !useBundledPanelByDefault() || len(bundledManagementHTML) == 0 {
		return nil, false
	}
	return bundledManagementHTML, true
}

func useBundledPanelByDefault() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MANAGEMENT_USE_BUNDLED_PANEL"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}
