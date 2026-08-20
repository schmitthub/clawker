//go:build darwin

package socketbridge

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func readListenerCredentials(connection *net.UnixConn) (int, int, error) {
	rawConnection, err := connection.SyscallConn()
	if err != nil {
		return 0, 0, fmt.Errorf("get raw Unix connection: %w", err)
	}
	var credentials *unix.Xucred
	var socketErr error
	if err := rawConnection.Control(func(fd uintptr) {
		credentials, socketErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, 0, fmt.Errorf("access Unix connection: %w", err)
	}
	if socketErr != nil {
		return 0, 0, fmt.Errorf("read LOCAL_PEERCRED: %w", socketErr)
	}
	if credentials.Ngroups < 1 {
		return 0, 0, errors.New("read LOCAL_PEERCRED: listener has no group")
	}
	return int(credentials.Uid), int(credentials.Groups[0]), nil
}
