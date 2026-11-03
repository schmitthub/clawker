package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/schmitthub/clawker/internal/storage"
)

// trustGroup is one covered key group: its display name and the key paths
// it covers. A `*` segment matches every entry of a map.
type trustGroup struct {
	group string
	paths []string
}

// trustedProjectKeys names the project-config keys the host CLI acts on
// before an agent container exists: they change what the host mounts,
// forwards, or runs. An agent can write the project files in a bind-mounted
// workspace, so a change here is a change the host must see before it acts.
// Keys that only change behavior inside the container (post_init, pre_run,
// env values, editor) are not covered.
func trustedProjectKeys() []trustGroup {
	return []trustGroup{
		// Docker socket, extra capabilities, host proxy, git credential
		// lanes, and the firewall rules the host sends to the control plane.
		{group: "security", paths: []string{"security"}},
		// Bundle sources the host fetches and runs during the image build.
		{group: "bundles", paths: []string{"bundles"}},
		// Build steps that run on the host Docker daemon during `docker
		// build`: root and user commands, file copies into the image, and
		// every raw Dockerfile inject point. Packages and stacks name
		// installs from configured sources and are not covered.
		{group: "build", paths: []string{
			"build.instructions.root_run",
			"build.instructions.user_run",
			"build.instructions.copy",
			"build.inject",
			"build.harnesses.*.inject",
		}},
		// Host env files read by the CLI and host env vars forwarded verbatim.
		{group: "agent", paths: []string{"agent.env_file", "agent.from_env"}},
		{group: "harnesses", paths: []string{"harnesses.*.env_file", "harnesses.*.from_env"}},
	}
}

// TrustedProjectKeyGroups returns the names of the covered key groups.
func TrustedProjectKeyGroups() []string {
	groups := trustedProjectKeys()
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		names = append(names, g.group)
	}
	return names
}

// trustLayerValue is one project layer's contribution to a group digest.
type trustLayerValue struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// ProjectTrustDigests returns one digest per covered key group, computed
// over the values the project-layer files (the files under the project
// root) set for that group. A group no project layer sets is absent from
// the map. Layers outside the project root (the user config dir) are not
// agent-writable and do not take part.
func ProjectTrustDigests(cfg Config) (map[string]string, error) {
	digests := map[string]string{}
	root := cfg.ProjectRoot()
	if root == "" {
		return digests, nil
	}
	layers := cfg.ProjectStore().Layers()
	for _, g := range trustedProjectKeys() {
		values := trustLayerValues(g, root, layers)
		if len(values) == 0 {
			continue
		}
		// encoding/json sorts map keys, so the encoding is canonical.
		encoded, err := json.Marshal(values)
		if err != nil {
			return nil, fmt.Errorf("config: encoding %s trust digest: %w", g.group, err)
		}
		sum := sha256.Sum256(encoded)
		digests[g.group] = hex.EncodeToString(sum[:])
	}
	return digests, nil
}

// trustLayerValues collects, per project layer under root, the subtree the
// group covers, sorted by layer path.
func trustLayerValues(g trustGroup, root string, layers []storage.LayerInfo) []trustLayerValue {
	var values []trustLayerValue
	for _, layer := range layers {
		if layer.Path == "" || !isUnderRoot(root, layer.Path) {
			continue
		}
		picked := map[string]any{}
		for _, p := range g.paths {
			if v, ok := pickPath(layer.Data, strings.Split(p, ".")); ok {
				deepMerge(picked, v)
			}
		}
		if len(picked) > 0 {
			values = append(values, trustLayerValue{Path: layer.Path, Value: picked})
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Path < values[j].Path })
	return values
}

// ChangedTrustGroups returns the groups whose digest differs between the
// recorded and the current map, in group order. A group present on one
// side only counts as changed.
func ChangedTrustGroups(recorded, current map[string]string) []string {
	var changed []string
	for _, g := range trustedProjectKeys() {
		if recorded[g.group] != current[g.group] {
			changed = append(changed, g.group)
		}
	}
	return changed
}

// isUnderRoot reports whether path is root or inside it. Both sides are
// symlink-resolved: the anchor comes in cwd form, the layer path resolved.
func isUnderRoot(root, path string) bool {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// pickPath returns the subtree of data at segs, kept under its own key
// path, or false when any segment is absent.
func pickPath(data any, segs []string) (map[string]any, bool) {
	m, isMap := data.(map[string]any)
	if !isMap {
		return nil, false
	}
	if segs[0] == "*" {
		return pickWildcard(m, segs[1:])
	}
	v, present := m[segs[0]]
	if !present {
		return nil, false
	}
	if len(segs) == 1 {
		return map[string]any{segs[0]: v}, true
	}
	sub, found := pickPath(v, segs[1:])
	if !found {
		return nil, false
	}
	return map[string]any{segs[0]: sub}, true
}

// pickWildcard keeps every entry of m that has the rest of the path.
func pickWildcard(m map[string]any, rest []string) (map[string]any, bool) {
	out := map[string]any{}
	for k, v := range m {
		if len(rest) == 0 {
			out[k] = v
			continue
		}
		if sub, found := pickPath(v, rest); found {
			out[k] = sub
		}
	}
	return out, len(out) > 0
}

// deepMerge grafts src into dst; nested maps merge, other values replace.
func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		sv, srcIsMap := v.(map[string]any)
		dv, dstIsMap := dst[k].(map[string]any)
		if srcIsMap && dstIsMap {
			deepMerge(dv, sv)
			continue
		}
		dst[k] = v
	}
}
