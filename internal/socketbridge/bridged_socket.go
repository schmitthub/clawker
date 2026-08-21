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
	HostPath string `json:"host_path"` //nolint:tagliatelle // The daemon protocol uses snake_case.
	Target   string `json:"target"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	Group    string `json:"group,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

// MarshalJSON writes the flat daemon registration shape.
func (s BridgedSocket) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(bridgedSocketJSON{
		HostPath: s.HostPath,
		Target:   s.Target,
		UID:      s.Identity.UID,
		GID:      s.Identity.GID,
		Group:    s.Group,
		Mode:     s.Mode,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal bridged socket: %w", err)
	}
	return data, nil
}

// UnmarshalJSON reads the flat daemon registration shape.
func (s *BridgedSocket) UnmarshalJSON(data []byte) error {
	var wire bridgedSocketJSON
	unmarshalErr := json.Unmarshal(data, &wire)
	if unmarshalErr != nil {
		return fmt.Errorf("unmarshal bridged socket: %w", unmarshalErr)
	}
	*s = BridgedSocket{
		HostPath: wire.HostPath,
		Target:   wire.Target,
		Identity: ListenerIdentity{UID: wire.UID, GID: wire.GID, Owner: "", Group: ""},
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
	writeErr := os.WriteFile(path, data, 0o600)
	if writeErr != nil {
		return fmt.Errorf("write bridged sockets file: %w", writeErr)
	}
	chmodErr := os.Chmod(path, 0o600)
	if chmodErr != nil {
		return fmt.Errorf("set bridged sockets file permissions: %w", chmodErr)
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
	unmarshalErr := json.Unmarshal(data, &sockets)
	if unmarshalErr != nil {
		return nil, fmt.Errorf("parse bridged sockets file: %w", unmarshalErr)
	}
	return sockets, nil
}
