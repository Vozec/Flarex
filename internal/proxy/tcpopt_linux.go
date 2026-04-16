//go:build linux

package proxy

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func tcpListenerControl(network, address string, c syscall.RawConn) error {
	var opErr error
	if err := c.Control(func(fd uintptr) {
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
			opErr = err
			return
		}
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
			opErr = err
			return
		}
	}); err != nil {
		return err
	}
	return opErr
}
