package informer

import "slices"

// matches reports whether r satisfies every constraint in f.
// A nil resource never matches — relation deltas carry nil Before/After
// and must be gated via subscriberFilterAllowsRelation, not here.
// An empty Filter matches every non-nil resource.
func (f Filter) matches(r *Resource) bool {
	if r == nil {
		return false
	}
	if len(f.Kinds) > 0 && !slices.Contains(f.Kinds, r.Kind) {
		return false
	}
	if len(f.Lifecycles) > 0 && !slices.Contains(f.Lifecycles, r.Lifecycle) {
		return false
	}
	if !f.Labels.matches(r.Labels) {
		return false
	}
	for k, v := range f.AttrsMatch {
		if got, ok := r.Attrs[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// matches reports whether labels satisfy the selector.
func (s LabelSelector) matches(labels map[string]string) bool {
	for k, want := range s.Equals {
		if got, ok := labels[k]; !ok || got != want {
			return false
		}
	}
	for k, bad := range s.NotEquals {
		// NotEquals passes if the key is absent OR has a different value.
		if got, ok := labels[k]; ok && got == bad {
			return false
		}
	}
	for _, k := range s.Exists {
		if _, ok := labels[k]; !ok {
			return false
		}
	}
	for _, k := range s.NotExists {
		if _, ok := labels[k]; ok {
			return false
		}
	}
	return true
}
