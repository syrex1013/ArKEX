package egress

import (
	"testing"
)

func TestControlPlaneDialerMarksConstant(t *testing.T) {
	if ControlPlaneMark == 0 {
		t.Fatal("ControlPlaneMark must be non-zero")
	}
	d := ControlPlaneDialer()
	if d == nil || d.Control == nil {
		t.Fatal("ControlPlaneDialer must set Control")
	}
	if d.Timeout <= 0 || d.KeepAlive <= 0 {
		t.Fatalf("unexpected dialer timeouts: %+v", d)
	}
}
