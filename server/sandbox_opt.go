package server

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Autumn-27/artex/sandbox"
)

// maybeSandboxManager returns a Docker sandbox manager when ARKEX_SANDBOX=1.
// Default off: ARTEX process isolation remains the baseline.
func maybeSandboxManager(dataDir, proxyAddr string) *sandbox.Manager {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("ARKEX_SANDBOX")))
	if v != "1" && v != "true" && v != "on" && v != "yes" {
		return nil
	}
	scratch := filepath.Join(dataDir, "sandbox-scratch")
	m, err := sandbox.NewManager(proxyAddr, scratch, os.Getenv("ARKEX_SANDBOX_NETWORK"))
	if err != nil {
		log.Printf("[sandbox] disabled: %v", err)
		return nil
	}
	log.Printf("[sandbox] enabled (scratch=%s proxy=%s)", scratch, proxyAddr)
	return m
}
