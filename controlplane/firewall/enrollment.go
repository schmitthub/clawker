package firewall

import (
	"context"
	"errors"
	"fmt"

	"github.com/moby/moby/api/types/events"

	"github.com/schmitthub/clawker/controlplane/dockerevents"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/storage"
)

// EnrollmentEntry is the per-container record the CLI pushes on
// FirewallEnable. It holds the enrolling project's values the CP cannot read
// itself: clawker.yaml lives in the host project tree, and the CP's own
// config sees only the config-dir layer.
type EnrollmentEntry struct {
	HostProxy bool `yaml:"host_proxy" label:"Host Proxy" desc:"Install the eBPF bypass route to the host proxy daemon for this container"`
}

// EnrollmentFile is the on-disk schema for the per-container enrollment
// records, keyed by canonical container ID.
type EnrollmentFile struct {
	Containers map[string]EnrollmentEntry `yaml:"containers,omitempty" label:"Containers" desc:"Enrollment records keyed by container ID"`
}

// Fields implements [storage.Schema] for EnrollmentFile.
//
//nolint:ireturn // storage.Schema mandates returning the FieldSet interface.
func (f EnrollmentFile) Fields() storage.FieldSet {
	return storage.NormalizeFields(f)
}

const enrollmentContainersField = "containers"

// EnrollmentStore is the domain facade over the persisted enrollment
// records. FirewallEnable writes a record before it enrolls, the FirewallInit
// re-enrollment sweep replays it, and the docker container/destroy event
// removes it. A container with no record is enrolled with no bypass.
//
//go:generate moq -rm -pkg mocks -out mocks/enrollment_store_mock.go . EnrollmentStore
type EnrollmentStore interface {
	// HostProxy returns the persisted host-proxy value for a container.
	// found is false when the container has no record.
	HostProxy(containerID string) (enabled, found bool, err error)
	// SetHostProxy persists the host-proxy value for a container,
	// replacing any earlier record.
	SetHostProxy(containerID string, enabled bool) error
	// Remove deletes a container's record. An absent record is not an error.
	Remove(containerID string) error
}

// enrollmentStoreImpl is the storage-backed EnrollmentStore. It embeds
// *storage.Store[EnrollmentFile] so the engine verbs stay reachable as the
// escape hatch; the type is unexported and only handed out as the interface.
//
// FirewallEnable writes from the RPC goroutine and the evict subscriber from
// the pub/sub delivery goroutine. Each writer touches its own container key,
// the engine verbs are each atomic, and a Write persists every staged field,
// so the two never lose each other's change.
type enrollmentStoreImpl struct {
	*storage.Store[EnrollmentFile]
}

// NewEnrollmentStore opens the enrollment table in the firewall data
// subdirectory, beside the egress rules and route identity stores. The
// constructor IS the load: a table that no longer decodes fails here, at CP
// startup.
//
//nolint:ireturn // returns the EnrollmentStore domain interface by design — enrollmentStoreImpl stays package-private
func NewEnrollmentStore(cfg config.Config) (EnrollmentStore, error) {
	dataDir, err := cfg.FirewallDataSubdir()
	if err != nil {
		return nil, fmt.Errorf("firewall: resolving data dir: %w", err)
	}
	store, err := storage.New[EnrollmentFile](
		storage.WithFilenames(consts.FirewallEnrollmentsFile),
		storage.WithDefaultFilename(consts.FirewallEnrollmentsFile),
		storage.WithPaths(dataDir),
		storage.WithLock(), // flock: defend against a second CP process racing the table.
	)
	if err != nil {
		return nil, fmt.Errorf("firewall: loading enrollment table: %w", err)
	}
	return newEnrollmentStoreImpl(store), nil
}

func newEnrollmentStoreImpl(store *storage.Store[EnrollmentFile]) *enrollmentStoreImpl {
	return &enrollmentStoreImpl{Store: store}
}

// HostProxy implements EnrollmentStore.
func (s *enrollmentStoreImpl) HostProxy(containerID string) (bool, bool, error) {
	entry, err := storage.Get[EnrollmentEntry](s.Store, enrollmentContainersField, containerID)
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("firewall: reading enrollment for %s: %w", containerID, err)
	}
	return entry.HostProxy, true, nil
}

// SetHostProxy implements EnrollmentStore. A failed Write restores the
// earlier record in memory: the replay sweep reads the in-memory table, and
// the next successful Write for any container persists every staged change,
// so a value that did not reach disk must not stay staged.
func (s *enrollmentStoreImpl) SetHostProxy(containerID string, enabled bool) error {
	prev, found, err := s.HostProxy(containerID)
	if err != nil {
		return err
	}
	entry := EnrollmentEntry{HostProxy: enabled}
	if setErr := s.Set([]string{enrollmentContainersField, containerID}, entry); setErr != nil {
		return fmt.Errorf("firewall: updating enrollment for %s: %w", containerID, setErr)
	}
	if writeErr := s.Write(); writeErr != nil {
		return errors.Join(
			fmt.Errorf("firewall: writing enrollment table: %w", writeErr),
			s.restore(containerID, prev, found),
		)
	}
	return nil
}

// Remove implements EnrollmentStore. A failed Write restores the record in
// memory, as SetHostProxy does.
func (s *enrollmentStoreImpl) Remove(containerID string) error {
	prev, found, err := s.HostProxy(containerID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if rmErr := s.Store.Remove(enrollmentContainersField, containerID); rmErr != nil {
		return fmt.Errorf("firewall: removing enrollment for %s: %w", containerID, rmErr)
	}
	if writeErr := s.Write(); writeErr != nil {
		return errors.Join(
			fmt.Errorf("firewall: writing enrollment table: %w", writeErr),
			s.restore(containerID, prev, true),
		)
	}
	return nil
}

// restore puts a container's record back to its value before a failed
// Write: the earlier entry when one existed, else no entry. The restore is
// staged like any change, so the next successful Write persists the value
// that is already on disk.
func (s *enrollmentStoreImpl) restore(containerID string, prev, found bool) error {
	var err error
	if found {
		err = s.Set([]string{enrollmentContainersField, containerID}, EnrollmentEntry{HostProxy: prev})
	} else if err = s.Store.Remove(enrollmentContainersField, containerID); errors.Is(err, storage.ErrKeyNotFound) {
		err = nil
	}
	if err != nil {
		return fmt.Errorf("firewall: restoring enrollment for %s after a failed write: %w", containerID, err)
	}
	return nil
}

// SubscribeEnrollmentEvict removes a container's enrollment record on the
// docker container/destroy event, the same event the agent registry evicts
// on. Stop, die, and kill do not evict: the container still exists and a
// later start must replay its record. The removal runs on the ActionQueue,
// the record's only writer, so it cannot interleave with a FirewallEnable
// for the same container. A failed removal is logged and the record stays
// until the next destroy or a later FirewallEnable overwrite.
func (h *Handler) SubscribeEnrollmentEvict(
	ctx context.Context,
	topic *pubsub.Topic[dockerevents.DockerEvent],
) {
	topic.Subscribe(func(evt pubsub.Event[dockerevents.DockerEvent]) {
		if ctx.Err() != nil {
			return
		}
		ev := evt.Payload
		if ev.Type != events.ContainerEventType || ev.Action != events.ActionDestroy {
			return
		}
		cid := ev.Actor.ID
		if cid == "" {
			return
		}
		_, err := h.submit(ActionEvict, func(_ context.Context) (any, error) {
			return nil, h.enrollments.Remove(cid)
		})
		switch {
		case err == nil:
			return
		case errors.Is(err, ErrQueueClosed):
			h.log.Info().Str("container_id", cid).
				Msg("firewall: enrollment evict-on-destroy skipped; queue closed (CP shutting down)")
		default:
			h.log.Warn().Err(err).Str("container_id", cid).
				Str("event", "firewall_enrollment_evict_failed").
				Msg("firewall: enrollment evict-on-destroy failed; record persists until the next destroy")
		}
	})
}
