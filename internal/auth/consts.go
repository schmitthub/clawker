package auth

import "github.com/schmitthub/clawker/internal/consts"

// Certificate Subject vocabulary for clawker-minted material. These CN
// values appear in minted certs and are pinned by unit tests; runtime
// verification is chain- and SAN-based (the runtime CN pins are
// consts.ContainerCP and consts.ContainerClawkerd, defined in
// internal/consts). The Organization is the brand string shared with
// resource naming.
const (
	// cliCACommonName is the CLI root CA's Subject CN.
	cliCACommonName = "clawker CLI CA"
	// infraCACommonName is the infra intermediate CA's Subject CN —
	// the trust anchor for the monitoring stack's otlp/infra receiver.
	infraCACommonName = "clawker infra intermediate CA"
	// otelCollectorCommonName is the otel-collector receiver server
	// cert's Subject CN.
	otelCollectorCommonName = "clawker-otel-collector"
	// certOrganization stamps Subject.Organization on every
	// clawker-minted certificate.
	certOrganization = consts.NamePrefix
)
