package bundler

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/schmitthub/clawker/internal/bundle"
)

// ShippedStackNames lists the stack components on the embedded floor.
func ShippedStackNames() []string {
	return bundle.FloorNames(bundle.ComponentStack)
}

// provenanceLine renders one resolved component's build-output provenance line,
// prefixed with the component type — e.g. "stack node ← project (…)",
// "harness claude ← built-in", or "stack acme.tools.node ← bundle acme.tools".
// The tier vocabulary comes from bundle.Provenance; the type prefix and the
// component's own address spelling (bare or dotted) frame it. A single Resolve
// reports only the resolved source, not the shadowed farther tiers, so this
// line names where the component came from without a "shadows …" clause.
func provenanceLine(comp bundle.Component) string {
	return comp.Type.String() + " " + comp.Provenance.Line(comp.Address.String())
}

// resolveStack resolves one declared stack address through the single
// resolution algorithm and loads its definition. A bare name resolves user
// loose > project loose > embedded floor; a qualified
// namespace.bundle.component address resolves from the installed/in-place
// bundle set. There is no bundle-embedded sibling lane and no project-registry
// lane — a harness's shipped sibling stack is referenced by its qualified
// self-address like any other bundle stack. An unresolvable address is a hard
// error, never a silent skip.
func resolveStack(r *bundle.Resolver, name string) (*StackDefinition, bundle.Component, error) {
	comp, err := r.Resolve(bundle.ComponentStack, name)
	if err != nil {
		return nil, bundle.Component{}, fmt.Errorf("resolve stack %q: %w", name, err)
	}
	def, loadErr := LoadStackDefinition(comp.Address.String(), comp.FS)
	if loadErr != nil {
		return nil, bundle.Component{}, loadErr
	}
	return def, comp, nil
}

// resolveStackDecls resolves a declaration list through the resolver,
// accumulating fragments in declaration order and one provenance line per
// non-floor resolution — a stack drawn from a loose convention dir or an
// installed bundle rather than the embedded floor is the notable case worth
// surfacing (it overrides or supplements what a plain build would use). The
// floor is the silent common case. dupIsError controls duplicate handling:
// build.stacks rejects a repeated address; a harness manifest renders it once,
// silently.
//
// Resolve reports only the winning tier, not the shadowed farther tiers (that
// full shadow listing is a bundle-list concern, and computing it here would
// require scanning the installed-bundle set — which must never block a
// floor-only build); so the line names the resolved source, e.g.
// "stack node ← project (…)" or "stack acme.tools.node ← bundle acme.tools".
func resolveStackDecls(
	r *bundle.Resolver,
	decls []string,
	dupIsError bool,
) ([]namedFragment, []namedFragment, []string, error) {
	seen := map[string]bool{}
	var defs []*StackDefinition
	var prov []string
	for _, name := range decls {
		if seen[name] {
			if dupIsError {
				return nil, nil, nil, fmt.Errorf("build.stacks: duplicate stack declaration %q", name)
			}
			continue
		}
		seen[name] = true
		def, comp, resolveErr := resolveStack(r, name)
		if resolveErr != nil {
			return nil, nil, nil, resolveErr
		}
		defs = append(defs, def)
		if comp.Provenance.Tier != bundle.TierFloor {
			prov = append(prov, provenanceLine(comp))
		}
	}
	root, user := splitFragments(defs)
	return root, user, prov, nil
}

// resolveProjectStacks resolves the project's build.stacks declarations for the
// BASE image. Every name resolves through the one algorithm (loose > floor for
// bare, installed for qualified); a repeated declaration is an error. Returns
// root- and user-scope fragment lists in declaration order plus the provenance
// lines of any resolution that shadowed a farther tier.
func resolveProjectStacks(
	r *bundle.Resolver,
	decls []string,
) ([]namedFragment, []namedFragment, []string, error) {
	return resolveStackDecls(r, decls, true)
}

// resolveHarnessStacks resolves a harness's stack dependencies for the HARNESS
// image through the same one algorithm as every other surface. Every declared
// address ALWAYS renders here with its resolved definition — there is no
// cross-stratum dedup against project-declared base stacks; a name the project
// also declares in build.stacks renders in both images, and fragment
// self-guards / apt idempotence / PATH shadowing own any interaction. A
// duplicate within the manifest list renders once, silently.
func resolveHarnessStacks(
	r *bundle.Resolver,
	harnessDecls []string,
) ([]namedFragment, []namedFragment, []string, error) {
	return resolveStackDecls(r, harnessDecls, false)
}

// namedFragment is one fragment of a definition, tagged with the
// definition's name for error context.
type namedFragment struct {
	name     string
	fragment string
}

// splitFragments partitions definitions' fragments into root- and
// user-scope lists, preserving declaration order. A definition contributes
// to either or both lists depending on which fragments it ships.
func splitFragments(defs []*StackDefinition) ([]namedFragment, []namedFragment) {
	var root, user []namedFragment
	for _, def := range defs {
		if def.RootFragment != "" {
			root = append(root, namedFragment{name: def.Name, fragment: def.RootFragment})
		}
		if def.UserFragment != "" {
			user = append(user, namedFragment{name: def.Name, fragment: def.UserFragment})
		}
	}
	return root, user
}

// renderStackSteps executes each fragment against the Dockerfile
// context, yielding the strings the template anchors splice in.
func renderStackSteps(fragments []namedFragment, tctx *DockerfileContext) ([]string, error) {
	var steps []string
	for _, f := range fragments {
		tmpl, err := template.New(f.name).Parse(f.fragment)
		if err != nil {
			return nil, fmt.Errorf("stack %q: parse fragment: %w", f.name, err)
		}
		var buf bytes.Buffer
		if execErr := tmpl.Execute(&buf, tctx); execErr != nil {
			return nil, fmt.Errorf("stack %q: render fragment: %w", f.name, execErr)
		}
		steps = append(steps, strings.TrimRight(buf.String(), "\n"))
	}
	return steps, nil
}
