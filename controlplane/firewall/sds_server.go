package firewall

import (
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	discoveryv3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	secretservice "github.com/envoyproxy/go-control-plane/envoy/service/secret/v3"
	lru "github.com/hashicorp/golang-lru/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// SDSSecretTypeURL is the xDS type URL for TLS secrets, carried on every
// delta discovery response the SDS server emits.
const SDSSecretTypeURL = "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.Secret"

// sdsCacheMaxEntries bounds the per-SNI mint cache. The secret name is the
// peer's ClientHello SNI: admitted() checks only wildcard-zone membership, so
// an agent can request an unbounded number of distinct admitted names, each a
// permanent cache entry. Unbounded, that is a peer-driven OOM of the CP —
// see the resilience contract in the package CLAUDE.md. The bound sits far
// above any real workload's distinct-host count; an evicted entry costs one
// re-mint on its next handshake, never a denial — admitted() stays the only
// gate.
const sdsCacheMaxEntries = 1024

// Sentinels for missing required SDS server dependencies.
var (
	ErrNilSDSStore = errors.New("firewall: NewSDSServer requires a non-nil EgressRulesStore")
)

// SDSServerDeps is the dependency set for the on-demand certificate SDS
// server.
type SDSServerDeps struct {
	// Store is the egress rules store. Every mint decision re-reads the
	// canonical rule set so a rule mutation takes effect on the next
	// handshake without any cross-subsystem invalidation.
	Store EgressRulesStore

	// CA is shared with the handler and stack. SDS only loads its CA pair.
	CA *CAStore

	// Log is the CP structured logger. Optional — defaults to Nop.
	Log *logger.Logger
}

// SDSServer implements the Envoy secret discovery service (delta protocol)
// behind the on-demand downstream certificate selector on wildcard MITM
// chains. During a TLS handshake Envoy pauses after the ClientHello, asks for
// a secret named by the SNI, and resumes with the returned certificate. The
// server mints a per-SNI leaf signed by the firewall MITM CA — this is what
// lets a wildcard rule cover hosts at every label depth, which the static
// [apex, *.apex] SAN pair cannot (RFC 6125 wildcards match one label; see
// issue #500).
//
// Fail closed: a name that is not admitted by a stored wildcard https/wss
// allow rule is answered with a resource removal, which fails the handshake.
// The chain's server_names match already restricts which SNIs reach the
// selector — this check is the mandatory in-Envoy-is-an-island redundancy,
// not the primary gate.
type SDSServer struct {
	secretservice.UnimplementedSecretDiscoveryServiceServer

	store EgressRulesStore
	ca    *CAStore
	log   *logger.Logger

	// mu serializes the check-then-mint sequence so concurrent requests for
	// one SNI mint once; the LRU's own lock only covers single operations.
	mu    sync.Mutex
	cache *lru.Cache[string, sdsCacheEntry]
}

// sdsCacheEntry is one minted per-SNI secret. caSerial ties the entry to the
// CA generation that signed it so a RotateCA invalidates the cache naturally.
type sdsCacheEntry struct {
	caSerial string
	version  string
	resource *anypb.Any
}

// NewSDSServer builds the SDS server. Constructor returns (nil, error) on
// missing required deps per the CP no-panic contract.
func NewSDSServer(deps SDSServerDeps) (*SDSServer, error) {
	if deps.Store == nil {
		return nil, ErrNilSDSStore
	}
	if deps.CA == nil {
		return nil, ErrNilCAStore
	}
	log := deps.Log
	if log == nil {
		log = logger.Nop()
	}
	srv := new(SDSServer)
	srv.store = deps.Store
	srv.ca = deps.CA
	srv.log = log
	cache, err := lru.New[string, sdsCacheEntry](sdsCacheMaxEntries)
	if err != nil {
		return nil, fmt.Errorf("firewall: sizing sds mint cache: %w", err)
	}
	srv.cache = cache
	return srv, nil
}

// DeltaSecrets serves the delta xDS stream the on-demand selector opens per
// secret. Each subscribe either mints (or serves the cached) per-SNI leaf, or
// reports the name as removed — Envoy fails the paused handshake on removal.
func (s *SDSServer) DeltaSecrets(stream secretservice.SecretDiscoveryService_DeltaSecretsServer) (err error) {
	// A handler panic must not unwind the CP serve goroutine — a dead CP
	// strands pinned eBPF state with no supervisor. Contain it to this stream.
	defer func() {
		if r := recover(); r != nil {
			s.log.Error().
				Interface("panic", r).
				Bytes("stack", debug.Stack()).
				Str("component", "firewall.sds").
				Str("event", "sds_handler_panic").
				Msg("SDS handler panicked; stream closed with codes.Internal")
			err = status.Error(codes.Internal, "sds handler failure")
		}
	}()

	var nonce uint64
	for {
		req, recvErr := stream.Recv()
		if recvErr != nil {
			// Stream end (Envoy closed, or transport error) — nothing to do.
			return nil
		}
		if len(req.GetResourceNamesSubscribe()) == 0 {
			// ACK/NACK or unsubscribe-only message — no response owed.
			continue
		}

		nonce++
		resp := s.deltaResponse(req.GetResourceNamesSubscribe())
		resp.Nonce = strconv.FormatUint(nonce, 10)
		if sendErr := stream.Send(resp); sendErr != nil {
			return fmt.Errorf("sending sds response: %w", sendErr)
		}
	}
}

// deltaResponse resolves one subscribe batch: every admitted name carries its
// (cached or freshly minted) secret; every other name is reported removed so
// Envoy fails the paused handshake closed.
func (s *SDSServer) deltaResponse(names []string) *discoveryv3.DeltaDiscoveryResponse {
	//nolint:exhaustruct,exhaustruct_v5 // The loop fills the response resources.
	resp := &discoveryv3.DeltaDiscoveryResponse{TypeUrl: SDSSecretTypeURL}
	for _, name := range names {
		entry, mintErr := s.secretFor(name)
		if mintErr != nil {
			s.log.Warn().Err(mintErr).
				Str("component", "firewall.sds").
				Str("sni", name).
				Str("event", "sds_secret_denied").
				Msg("SNI not admitted for on-demand mint — handshake will fail closed")
			resp.RemovedResources = append(resp.RemovedResources, name)
			continue
		}
		//nolint:exhaustruct,exhaustruct_v5 // sparse wire message — name/version/resource is the whole delta payload
		resp.Resources = append(resp.Resources, &discoveryv3.Resource{
			Name:     name,
			Version:  entry.version,
			Resource: entry.resource,
		})
	}
	return resp
}

// secretFor returns the cached or freshly minted secret for one SNI, or an
// error when the name is not admitted.
func (s *SDSServer) secretFor(name string) (sdsCacheEntry, error) {
	sni := strings.ToLower(strings.TrimSuffix(name, "."))
	if err := validateSNIHostname(sni); err != nil {
		return sdsCacheEntry{}, err
	}
	if err := s.admitted(sni); err != nil {
		return sdsCacheEntry{}, err
	}

	caCert, caKey, err := s.ca.Load()
	if err != nil {
		return sdsCacheEntry{}, fmt.Errorf("loading MITM CA: %w", err)
	}
	caSerial := caCert.SerialNumber.String()

	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.cache.Get(sni); ok && entry.caSerial == caSerial {
		return entry, nil
	}

	certPEM, keyPEM, err := GenerateSNICert(caCert, caKey, sni)
	if err != nil {
		return sdsCacheEntry{}, fmt.Errorf("minting sni leaf: %w", err)
	}
	tlsCert := new(tlsv3.TlsCertificate)
	tlsCert.CertificateChain = inlineDataSource(certPEM)
	tlsCert.PrivateKey = inlineDataSource(keyPEM)
	secret := new(tlsv3.Secret)
	secret.Name = name
	secret.Type = &tlsv3.Secret_TlsCertificate{TlsCertificate: tlsCert}
	resource, err := anypb.New(secret)
	if err != nil {
		return sdsCacheEntry{}, fmt.Errorf("marshalling secret: %w", err)
	}
	entry := sdsCacheEntry{caSerial: caSerial, version: caSerial + "-" + sni, resource: resource}
	s.cache.Add(sni, entry)
	s.log.Info().
		Str("component", "firewall.sds").
		Str("sni", sni).
		Str("event", "sds_secret_minted").
		Msg("minted on-demand MITM leaf for SNI")
	return entry, nil
}

// inlineDataSource wraps raw bytes as an Envoy inline data source.
func inlineDataSource(b []byte) *corev3.DataSource {
	src := new(corev3.DataSource)
	src.Specifier = &corev3.DataSource_InlineBytes{InlineBytes: b}
	return src
}

// admitted reports whether the SNI is covered by a stored wildcard https/wss
// rule whose action is allow. The longest matching zone decides — the same
// longest-zone-wins semantic CoreDNS applies — so a deny wildcard nested
// under an allow wildcard still denies.
func (s *SDSServer) admitted(sni string) error {
	rules, _, err := s.store.Rules()
	if err != nil {
		return fmt.Errorf("reading rules store: %w", err)
	}
	bestLen := -1
	bestAllow := false
	for _, r := range rules {
		if !matchesWildcardTLSZone(r, sni) {
			continue
		}
		if zoneLen := len(normalizeDomain(r.Dst)); zoneLen > bestLen {
			bestLen = zoneLen
			bestAllow = !isDenyAction(r.Action)
		}
	}
	if bestLen < 0 {
		return fmt.Errorf("sni %q matches no wildcard https/wss rule zone", sni)
	}
	if !bestAllow {
		return fmt.Errorf("sni %q is denied by its longest matching wildcard zone", sni)
	}
	return nil
}

func matchesWildcardTLSZone(rule config.EgressRule, sni string) bool {
	if !isWildcardDomain(rule.Dst) {
		return false
	}
	if proto := strings.ToLower(rule.Proto); proto != protoHTTPS && proto != protoWSS {
		return false
	}
	apex := normalizeDomain(rule.Dst)
	return sni == apex || strings.HasSuffix(sni, "."+apex)
}
