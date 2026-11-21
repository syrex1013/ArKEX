package egress

import (
	"net"
	"time"
)

// ControlPlaneMark is the SO_MARK applied to LLM (and other control-plane)
// sockets so the kernel WireGuard kill-switch can send them out the host
// gateway instead of the tunnel. Must stay in sync with the iptables/ip-rule
// install in package wgkernel.
const ControlPlaneMark = 0x41784c4d // "AxLM"

// ControlPlaneDialer returns a Dialer that marks sockets with ControlPlaneMark.
// On non-Linux builds the mark is a no-op; on Linux with the kill-switch up,
// marked packets leave via the host default gateway (never Mullvad).
func ControlPlaneDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   controlPlaneMarkControl,
	}
}
