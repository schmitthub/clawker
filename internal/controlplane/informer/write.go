package informer

import (
	"context"
	"fmt"
	"time"
)

// Upsert inserts or merges u. The transition t is appended to the
// resource history and emitted as a delta (DeltaAdded for a new key,
// DeltaUpdated for an existing one).
//
// Identity: (u.Kind, u.ID) must be non-empty. Identity uniqueness is
// composite — the same raw ID may legitimately appear under different
// Kinds without collision.
//
// Blocks until the write is committed. Returns ErrClosed if the
// informer is shut down or ctx.Err() if ctx cancels before or during
// enqueue.
func (i *Informer) Upsert(ctx context.Context, u ResourceUpdate, t Transition) error {
	if err := validateKey(u.Kind, u.ID); err != nil {
		return err
	}
	result := make(chan opResult, 1)
	op := op{
		fn: func(s *store, now time.Time) (Delta, bool) {
			return s.upsert(u, withNow(t, now), now)
		},
		result: result,
	}
	if err := i.submit(ctx, op); err != nil {
		return err
	}
	_, err := waitOp(ctx, result)
	return err
}

// Patch applies fn to the resource under key atomically. fn runs on
// the writer goroutine under the store write lock; it must not call
// back into the informer and must not retain the pointer.
//
// Patching changes Lifecycle/Labels/Attrs/history only — Kind and ID
// are re-anchored to the key after fn returns. No-op if the key is
// unknown, with no delta.
func (i *Informer) Patch(ctx context.Context, key Key, fn func(*Resource), t Transition) error {
	if err := validateKey(key.Kind, key.ID); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("informer: Patch: nil fn")
	}
	result := make(chan opResult, 1)
	op := op{
		fn: func(s *store, now time.Time) (Delta, bool) {
			return s.patch(key, fn, withNow(t, now), now)
		},
		result: result,
	}
	if err := i.submit(ctx, op); err != nil {
		return err
	}
	_, err := waitOp(ctx, result)
	return err
}

// Remove soft-deletes the resource under key: Lifecycle is set to
// LifecycleGone, the transition is recorded, and a DeltaRemoved is
// emitted. The resource itself stays in the store with its final
// state and history for forensic reads. No-op if the key is unknown
// or already LifecycleGone.
func (i *Informer) Remove(ctx context.Context, key Key, t Transition) error {
	if err := validateKey(key.Kind, key.ID); err != nil {
		return err
	}
	result := make(chan opResult, 1)
	op := op{
		fn: func(s *store, now time.Time) (Delta, bool) {
			return s.remove(key, withNow(t, now), now)
		},
		result: result,
	}
	if err := i.submit(ctx, op); err != nil {
		return err
	}
	_, err := waitOp(ctx, result)
	return err
}

// LinkRelation inserts a directed edge rel.From → rel.To of rel.Kind.
// Idempotent refresh: re-linking an existing edge updates LastSeen
// and merges Attrs without emitting a second DeltaRelationAdded.
//
// Both endpoints must have non-empty Kind and ID. The endpoints need
// not exist as resources — feeders may link ahead of discovering
// either side. Orphan edges are valid and discoverable via
// Stats().Relations.
func (i *Informer) LinkRelation(ctx context.Context, rel Relation) error {
	if err := validateKey(rel.From.Kind, rel.From.ID); err != nil {
		return fmt.Errorf("informer: LinkRelation: from: %w", err)
	}
	if err := validateKey(rel.To.Kind, rel.To.ID); err != nil {
		return fmt.Errorf("informer: LinkRelation: to: %w", err)
	}
	if rel.Kind == "" {
		return fmt.Errorf("informer: LinkRelation: empty relation kind")
	}
	result := make(chan opResult, 1)
	op := op{
		fn: func(s *store, now time.Time) (Delta, bool) {
			return s.linkRelation(rel, now)
		},
		result: result,
	}
	if err := i.submit(ctx, op); err != nil {
		return err
	}
	_, err := waitOp(ctx, result)
	return err
}

// UnlinkRelation removes a directed edge. No-op if absent, with no
// delta.
func (i *Informer) UnlinkRelation(ctx context.Context, from, to Key, kind string) error {
	if err := validateKey(from.Kind, from.ID); err != nil {
		return fmt.Errorf("informer: UnlinkRelation: from: %w", err)
	}
	if err := validateKey(to.Kind, to.ID); err != nil {
		return fmt.Errorf("informer: UnlinkRelation: to: %w", err)
	}
	if kind == "" {
		return fmt.Errorf("informer: UnlinkRelation: empty relation kind")
	}
	result := make(chan opResult, 1)
	op := op{
		fn: func(s *store, now time.Time) (Delta, bool) {
			return s.unlinkRelation(from, to, kind)
		},
		result: result,
	}
	if err := i.submit(ctx, op); err != nil {
		return err
	}
	_, err := waitOp(ctx, result)
	return err
}

func validateKey(kind, id string) error {
	if kind == "" {
		return fmt.Errorf("informer: empty kind")
	}
	if id == "" {
		return fmt.Errorf("informer: empty id")
	}
	return nil
}

// withNow sets t.At if zero. Feeders that want to record an earlier
// observation time (e.g. Docker event timestamp) set t.At explicitly
// and it is preserved.
func withNow(t Transition, now time.Time) Transition {
	if t.At.IsZero() {
		t.At = now
	}
	return t
}
