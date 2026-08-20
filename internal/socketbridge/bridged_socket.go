package socketbridge

import (
	"encoding/json"
	"fmt"
	"os"
)

// BridgedSocket is one approved host socket registration for a container.
type BridgedSocket struct {
	HostPath string           `json:"-"`
	Target   string           `json:"-"`
	Identity ListenerIdentity `json:"-"`
	Group    string           `json:"-"`
	Mode     string           `json:"-"`
}

type bridgedSocketJSON struct {
	HostPath string `json:"host_path"`
	Target   string `json:"target"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	Group    string `json:"group,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

// MarshalJSON writes the flat daemon registration shape.
func (s BridgedSocket) MarshalJSON() ([]byte, error) {
	return json.Marshal(bridgedSocketJSON{
		HostPath: s.HostPath,
		Target:   s.Target,
		UID:      s.Identity.UID,
		GID:      s.Identity.GID,
		Group:    s.Group,
		Mode:     s.Mode,
	})
}

// UnmarshalJSON reads the flat daemon registration shape.
func (s *BridgedSocket) UnmarshalJSON(data []byte) error {
	var wire bridgedSocketJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*s = BridgedSocket{
		HostPath: wire.HostPath,
		Target:   wire.Target,
		Identity: ListenerIdentity{UID: wire.UID, GID: wire.GID},
		Group:    wire.Group,
		Mode:     wire.Mode,
	}
	return nil
}

// WriteBridgedSocketsFile writes daemon registrations with owner-only access.
func WriteBridgedSocketsFile(path string, sockets []BridgedSocket) error {
	if sockets == nil {
		sockets = []BridgedSocket{}
	}
	data, err := json.Marshal(sockets)
	if err != nil {
		return fmt.Errorf("marshal bridged sockets: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write bridged sockets file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set bridged sockets file permissions: %w", err)
	}
	return nil
}

// ReadBridgedSocketsFile reads daemon registrations.
func ReadBridgedSocketsFile(path string) ([]BridgedSocket, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bridged sockets file: %w", err)
	}
	var sockets []BridgedSocket
	if err := json.Unmarshal(data, &sockets); err != nil {
		return nil, fmt.Errorf("parse bridged sockets file: %w", err)
	}
	return sockets, nil
}
