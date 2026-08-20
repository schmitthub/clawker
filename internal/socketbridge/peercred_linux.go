//go:build linux

package socketbridge

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func readListenerCredentials(connection *net.UnixConn) (int, int, error) {
	rawConnection, err := connection.SyscallConn()
	if err != nil {
		return 0, 0, fmt.Errorf("get raw Unix connection: %w", err)
	}
	var credentials *unix.Ucred
	var socketErr error
	if err := rawConnection.Control(func(fd uintptr) {
		credentials, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, 0, fmt.Errorf("access Unix connection: %w", err)
	}
	if socketErr != nil {
		return 0, 0, fmt.Errorf("read SO_PEERCRED: %w", socketErr)
	}
	return int(credentials.Uid), int(credentials.Gid), nil
}
