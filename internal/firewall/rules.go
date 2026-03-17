package firewall

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/storage"
)

// NewRulesStore creates a storage.Store[EgressRulesFile] for egress-rules.yaml.
// The store uses the firewall data subdirectory for file discovery.
// For concurrent safety, use UpdateRules/RemoveRules which handle their own locking.
func NewRulesStore(cfg config.Config) (*storage.Store[EgressRulesFile], error) {
	dataDir, err := cfg.FirewallDataSubdir()
	if err != nil {
		return nil, fmt.Errorf("firewall: resolving data dir: %w", err)
	}
	return storage.NewStore[EgressRulesFile](
		storage.WithFilenames(cfg.EgressRulesFileName()),
		storage.WithPaths(dataDir),
	)
}

// ruleKey returns the dedup key for an egress rule: dst:proto:port.
// Proto defaults to "tls", port defaults to 0 (matching NormalizeRules behavior).
func ruleKey(r config.EgressRule) string {
	proto := r.Proto
	if proto == "" {
		proto = "tls"
	}
	return fmt.Sprintf("%s:%s:%d", r.Dst, proto, r.Port)
}

// rulesLockPath returns the flock path for the egress rules file.
func rulesLockPath(cfg config.Config) (string, error) {
	dataDir, err := cfg.FirewallDataSubdir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, cfg.EgressRulesFileName()+".lock"), nil
}

// withRulesLock acquires an advisory flock for the egress rules file and
// calls fn with a freshly loaded store. This ensures the entire
// read-diff-write cycle is atomic across processes.
func withRulesLock(cfg config.Config, fn func(store *storage.Store[EgressRulesFile]) error) error {
	lockPath, err := rulesLockPath(cfg)
	if err != nil {
		return fmt.Errorf("resolving lock path: %w", err)
	}

	fl := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	locked, err := fl.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return fmt.Errorf("acquiring lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("timed out acquiring lock on %s", lockPath)
	}
	defer fl.Unlock() //nolint:errcheck

	// Create a fresh store (reads current disk state) within the lock.
	store, err := NewRulesStore(cfg)
	if err != nil {
		return fmt.Errorf("creating store: %w", err)
	}

	return fn(store)
}

// UpdateRules appends incoming rules that are not already present.
// Returns true if any new rules were written.
func UpdateRules(cfg config.Config, incoming []config.EgressRule) (bool, error) {
	var written bool

	err := withRulesLock(cfg, func(store *storage.Store[EgressRulesFile]) error {
		current := store.Read()

		// Index existing rules; reuse the same map to deduplicate incoming.
		known := make(map[string]struct{}, len(current.Rules)+len(incoming))
		for _, r := range current.Rules {
			known[ruleKey(r)] = struct{}{}
		}

		var newRules []config.EgressRule
		for _, r := range incoming {
			key := ruleKey(r)
			if _, exists := known[key]; exists {
				continue
			}
			known[key] = struct{}{}
			newRules = append(newRules, r)
		}

		if len(newRules) == 0 {
			return nil
		}

		if err := store.Set(func(f *EgressRulesFile) {
			f.Rules = append(f.Rules, newRules...)
		}); err != nil {
			return fmt.Errorf("updating rules: %w", err)
		}

		if err := store.Write(); err != nil {
			return fmt.Errorf("writing rules: %w", err)
		}

		written = true
		return nil
	})

	if err != nil {
		return false, fmt.Errorf("firewall: %w", err)
	}
	return written, nil
}

// RemoveRules removes rules matching dst+proto+port from the store.
func RemoveRules(cfg config.Config, toRemove []config.EgressRule) error {
	if len(toRemove) == 0 {
		return nil
	}

	return withRulesLock(cfg, func(store *storage.Store[EgressRulesFile]) error {
		// Build set of keys to remove.
		removeSet := make(map[string]struct{}, len(toRemove))
		for _, r := range toRemove {
			removeSet[ruleKey(r)] = struct{}{}
		}

		current := store.Read()
		filtered := make([]config.EgressRule, 0, len(current.Rules))
		for _, r := range current.Rules {
			if _, remove := removeSet[ruleKey(r)]; !remove {
				filtered = append(filtered, r)
			}
		}

		if len(filtered) == len(current.Rules) {
			return nil // nothing removed, skip write
		}

		if err := store.Set(func(f *EgressRulesFile) {
			f.Rules = filtered
		}); err != nil {
			return fmt.Errorf("removing rules: %w", err)
		}

		return store.Write()
	})
}

// ReadRules returns the current egress rules from the store.
// It does not acquire the advisory flock. Callers needing
// read-modify-write atomicity should use UpdateRules or RemoveRules.
func ReadRules(cfg config.Config) ([]config.EgressRule, error) {
	store, err := NewRulesStore(cfg)
	if err != nil {
		return nil, err
	}
	return store.Read().Rules, nil
}
