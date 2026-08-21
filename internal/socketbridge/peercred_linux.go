//go:build linux

package socketbridge

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func readListenerCredentials(connection *net.UnixConn) (int, int, error) {
	rawConnection, connectionErr := connection.SyscallConn()
	if connectionErr != nil {
		return 0, 0, fmt.Errorf("get raw Unix connection: %w", connectionErr)
	}
	var credentials *unix.Ucred
	var socketErr error
	controlErr := rawConnection.Control(func(fd uintptr) {
		credentials, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if controlErr != nil {
		return 0, 0, fmt.Errorf("access Unix connection: %w", controlErr)
	}
	if socketErr != nil {
		return 0, 0, fmt.Errorf("read SO_PEERCRED: %w", socketErr)
	}
	return int(credentials.Uid), int(credentials.Gid), nil
}
