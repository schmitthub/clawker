package mocks

// NewMockManager creates a SocketBridgeManagerMock with no-op defaults.
// Unlike bare moq (which panics on nil Func fields), this wires safe defaults
// so tests only need to override the methods they care about.
func NewMockManager() *SocketBridgeManagerMock {
	return &SocketBridgeManagerMock{
		EnsureBridgeFunc: func(containerID string, gpgEnabled bool) error { return nil },
		StopBridgeFunc:   func(containerID string) error { return nil },
		StopAllFunc:      func() error { return nil },
		IsRunningFunc:    func(containerID string) bool { return false },
	}
}

// CalledWith returns true if the given method was called with the given containerID.
// Convenience wrapper over moq's typed call slices.
func CalledWith(mock *SocketBridgeManagerMock, method, containerID string) bool {
	switch method {
	case "EnsureBridge":
		for _, c := range mock.EnsureBridgeCalls() {
			if c.ContainerID == containerID {
				return true
			}
		}
	case "StopBridge":
		for _, c := range mock.StopBridgeCalls() {
			if c.ContainerID == containerID {
				return true
			}
		}
	case "IsRunning":
		for _, c := range mock.IsRunningCalls() {
			if c.ContainerID == containerID {
				return true
			}
		}
	}
	return false
}
