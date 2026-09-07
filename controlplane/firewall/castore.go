package firewall

import (
	"crypto/ecdsa"
	"crypto/x509"
	"fmt"
	"sync"

	"github.com/schmitthub/clawker/internal/config"
)

// CAStore owns the MITM CA. The handler, stack, and SDS server must
// share one store so certificate reads cannot overlap CA replacement.
type CAStore struct {
	mu        sync.RWMutex
	certDirFn func() (string, error)
}

// NewCAStore constructs a store without reading or writing certificates.
func NewCAStore(certDirFn func() (string, error)) (*CAStore, error) {
	if certDirFn == nil {
		return nil, ErrNilCACertDirFn
	}
	store := new(CAStore)
	store.certDirFn = certDirFn
	return store, nil
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
	return RotateCA(certDir, rules)
}
