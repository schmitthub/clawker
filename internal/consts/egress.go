package consts

// Egress rule vocabulary — the protocol and action tokens accepted in
// clawker.yaml (security.firewall.rules), on the `clawker firewall add`
// CLI, and in the persisted egress-rules store. The firewall config
// generators (Envoy, CoreDNS) branch on these same tokens; no layer
// re-spells them.
const (
	EgressProtoHTTPS = "https"
	EgressProtoHTTP  = "http"
	EgressProtoWS    = "ws"
	EgressProtoWSS   = "wss"
	EgressProtoSSH   = "ssh"
	EgressProtoTCP   = "tcp"
	EgressProtoUDP   = "udp"
)

// Egress rule actions.
const (
	EgressActionAllow = "allow"
	EgressActionDeny  = "deny"
)

// EgressPortHTTPS is the EgressProtoHTTPS default destination port.
const EgressPortHTTPS = "443"
