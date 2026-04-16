//go:build !linux

package proxy

import "syscall"

func tcpListenerControl(network, address string, c syscall.RawConn) error {
	return nil
}
