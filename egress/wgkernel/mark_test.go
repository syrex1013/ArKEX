package wgkernel

import (
	"testing"

	"github.com/Autumn-27/artex/egress"
)

func TestControlPlaneMarkNonZero(t *testing.T) {
	if egress.ControlPlaneMark == 0 {
		t.Fatal("egress.ControlPlaneMark must stay non-zero; kill-switch ACCEPT depends on it")
	}
	if controlPlaneTable != 100 {
		t.Fatalf("controlPlaneTable=%d, want 100", controlPlaneTable)
	}
}
