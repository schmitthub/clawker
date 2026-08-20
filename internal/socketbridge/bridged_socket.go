package socketbridge

// BridgedSocket is one approved host socket registration for a container.
type BridgedSocket struct {
	HostPath string
	Target   string
	Identity ListenerIdentity
	Group    string
	Mode     string
}
