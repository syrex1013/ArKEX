//go:build linux

package egress

import "syscall"

// controlPlaneMarkControl stamps SO_MARK so wgkernel's fwmark rule can steer
// the packet onto the direct (non-tunnel) routing table.
func controlPlaneMarkControl(network, address string, c syscall.RawConn) error {
	var sockErr error
	if err := c.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_MARK, ControlPlaneMark)
	}); err != nil {
		return err
	}
	return sockErr
}
