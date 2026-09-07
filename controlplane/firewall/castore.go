package firewall

import (
	"crypto/ecdsa"
	"crypto/x509"
	"fmt"
	"sync"

	"github.com/schmitthub/clawker/controlplane/sdscerts"
	"github.com/schmitthub/clawker/internal/config"
)

// CAStore owns the MITM CA. The handler, stack, and SDS server must
// share one store so certificate reads cannot overlap CA replacement.
type CAStore struct {
	mu        sync.RWMutex
	certDirFn func() (string, error)
	// reader is the identity that loads the per-domain leaves from disk —
	// the Envoy sibling (consts.EnvoyUID), never CP root. Every domain pair
	// the store regenerates is published under this owner.
	reader sdscerts.FileOwner
}

// NewCAStore constructs a store without reading or writing certificates.
// reader is the domain-cert reader identity; see RegenerateDomainCerts.
func NewCAStore(certDirFn func() (string, error), reader sdscerts.FileOwner) (*CAStore, error) {
	if certDirFn == nil {
		return nil, ErrNilCACertDirFn
	}
	store := new(CAStore)
	store.certDirFn = certDirFn
	store.reader = reader
	return store, nil
}

// Reader is the domain-cert reader identity this store publishes leaves for.
func (s *CAStore) Reader() sdscerts.FileOwner {
	return s.reader
}

// Load reads an existing CA pair. It never creates certificate files.
func (s *CAStore) Load() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	certDir, err := s.certDirFn()
	if err != nil {
		return nil, nil, fmt.Errorf("resolving CA directory: %w", err)
	}
	return LoadCA(certDir)
}

// Ensure loads the CA or creates a pair if it is absent.
func (s *CAStore) Ensure() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	certDir, err := s.certDirFn()
	if err != nil {
		return nil, nil, fmt.Errorf("resolving CA directory: %w", err)
	}
	return EnsureCA(certDir)
}

// Rotate replaces the CA and regenerates the certificates for rules.
func (s *CAStore) Rotate(rules []config.EgressRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	certDir, err := s.certDirFn()
	if err != nil {
		return fmt.Errorf("resolving CA directory: %w", err)
	}
	return RotateCA(certDir, rules, s.reader)
}
