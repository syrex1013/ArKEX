//go:build !linux

package egress

import "syscall"

// controlPlaneMarkControl is a no-op off Linux (no SO_MARK / no wgkernel).
func controlPlaneMarkControl(network, address string, c syscall.RawConn) error {
	return nil
}
