package firewall

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/crypto/x509roots/fallback/bundle"

	"github.com/schmitthub/clawker/controlplane/sdscerts"
)

// UpstreamCABundleFile is the root bundle in the firewall certs directory that
// Envoy validates re-encrypted upstream TLS against. CP writes it from the
// Mozilla root store carried by the x509roots/fallback module, so the trust
// anchor follows the root program through dependency updates instead of the
// Envoy image's Debian ca-certificates package. The Envoy image bundle still
// trusted the roots that issued the compromised eMudhra intermediates after
// every root program had removed them.
const UpstreamCABundleFile = "upstream-ca.crt"

var errEmptyUpstreamCABundle = errors.New("firewall: upstream CA bundle has no roots")

// WriteUpstreamCABundle publishes the Mozilla root store into certDir for
// Envoy's upstream validation. Roots with NSS constraints are left out: Envoy
// cannot apply a constraint, and an unconstrained copy would trust more than
// the root program does. The file goes to the domain-cert reader with the
// same atomic write as the per-domain certificates.
func WriteUpstreamCABundle(certDir string, reader sdscerts.FileOwner) error {
	var buf bytes.Buffer
	count := 0
	for root := range bundle.Roots() {
		if root.Constraint != nil {
			continue
		}
		buf.Write(encodePEM(pemBlockCertificate, root.Certificate))
		count++
	}
	if count == 0 {
		return errEmptyUpstreamCABundle
	}
	if err := writePEMAtomic(filepath.Join(certDir, UpstreamCABundleFile), buf.Bytes(), reader); err != nil {
		return fmt.Errorf("writing upstream CA bundle: %w", err)
	}
	return nil
}
