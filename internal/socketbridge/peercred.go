package socketbridge

import (
	"errors"
	"fmt"
	"net"
	"os/user"
	"strconv"
)

// ReadListenerIdentity connects to a Unix socket, reads the listener's peer
// credentials, resolves display names, and closes the probe connection.
func ReadListenerIdentity(path string) (ListenerIdentity, error) {
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return ListenerIdentity{}, fmt.Errorf("read listener identity for %q: connect: %w", path, err)
	}
	uid, gid, credentialErr := readListenerCredentials(connection)
	closeErr := connection.Close()
	if credentialErr != nil {
		if closeErr != nil {
			return ListenerIdentity{}, errors.Join(
				fmt.Errorf("read listener identity for %q: %w", path, credentialErr),
				fmt.Errorf("close listener identity probe for %q: %w", path, closeErr),
			)
		}
		return ListenerIdentity{}, fmt.Errorf("read listener identity for %q: %w", path, credentialErr)
	}
	if closeErr != nil {
		return ListenerIdentity{}, fmt.Errorf("close listener identity probe for %q: %w", path, closeErr)
	}

	identity := ListenerIdentity{UID: uid, GID: gid, Owner: "", Group: ""}
	if owner, lookupErr := user.LookupId(strconv.Itoa(uid)); lookupErr == nil {
		identity.Owner = owner.Username
	}
	if group, lookupErr := user.LookupGroupId(strconv.Itoa(gid)); lookupErr == nil {
		identity.Group = group.Name
	}
	return identity, nil
}
