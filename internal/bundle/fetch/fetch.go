// Package fetch is the bundle-model git fetch/clone leaf. It clones a declared
// bundle source into a local directory and resolves a ref to a commit SHA. It
// imports only stdlib and go-git/go-billy — never internal/config or any other
// clawker internal package — so the cache/install engine (internal/bundle) can
// depend on it without dragging config into a transport leaf.
//
// The engine talks to real git servers by URL: production uses the go-git
// implementation returned by [NewFetcher]; integration tests point a declared
// source at an in-process git fixture server (see internal/bundle/bundletest)
// and exercise the same code path. The [Fetcher] interface exists so the go-git
// v6 (alpha) backend can be swapped for a shell-out implementation later — it is
// not a test seam (tests use the real fetcher against a real server).
package fetch

import (
	"context"
	"errors"
	"fmt"

	gogit "github.com/go-git/go-git/v6"
	gogitconfig "github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/client"
	"github.com/go-git/go-git/v6/storage/memory"
)

// remoteName is the single remote every fetched bundle repository is wired to.
const remoteName = "origin"

// shaFetchRef is the local ref a sha-pinned fetch writes the wanted commit to.
const shaFetchRef = "refs/bundle/fetch-head"

// CloneOptions parameterizes a single bundle fetch. SHA pins an exact commit
// and takes precedence when both pins are set (the caller resolves that
// precedence and passes only the winner); Ref pins a branch or tag; neither
// set means the source is unpinned and the remote's default branch (HEAD) is
// fetched. Dir is an empty target directory the content is cloned into.
type CloneOptions struct {
	URL string
	Ref string
	SHA string
	Dir string
}

// Fetcher resolves refs and clones bundle sources. The go-git implementation is
// returned by [NewFetcher]; a future shell-out implementation can satisfy the
// same interface.
type Fetcher interface {
	// ResolveRef resolves a branch or tag name on a remote to its commit SHA,
	// dereferencing annotated tags to the commit they point at; an empty ref
	// resolves the remote's HEAD (the default branch tip). It performs a
	// remote ref listing only — no clone, no local writes.
	ResolveRef(ctx context.Context, url, ref string) (string, error)
	// Clone fetches the pinned source into opts.Dir and returns the resolved
	// commit SHA that was checked out (the SHA verbatim for a sha pin, or the
	// commit a ref resolved to). opts.Dir must be empty.
	Clone(ctx context.Context, opts CloneOptions) (string, error)
}

// NewFetcher returns the production go-git-backed fetcher. It satisfies
// [Fetcher], the swap seam a future shell-out implementation would fill.
func NewFetcher() GitFetcher { return GitFetcher{} }

// GitFetcher is the go-git-backed Fetcher.
type GitFetcher struct{}

// ResolveRef resolves ref to a commit SHA via a remote ref listing.
func (f GitFetcher) ResolveRef(ctx context.Context, url, ref string) (string, error) {
	hash, _, err := f.resolveRefFull(ctx, url, ref)
	if err != nil {
		return "", err
	}
	return hash.String(), nil
}

// resolveRefFull lists the remote's refs and matches ref against heads then
// tags (peeled), returning the commit hash and the full reference name a
// single-branch clone can check out.
func (f GitFetcher) resolveRefFull(
	ctx context.Context, url, ref string,
) (plumbing.Hash, plumbing.ReferenceName, error) {
	var refs []*plumbing.Reference
	err := withHTTPAuthRetry(ctx, url, func(opts []client.Option) error {
		rem := gogit.NewRemote(memory.NewStorage(), &gogitconfig.RemoteConfig{
			Name: remoteName, URLs: []string{url}, Mirror: false, Fetch: nil,
		})
		listed, listErr := rem.ListContext(ctx, &gogit.ListOptions{
			ClientOptions: opts, PeelingOption: gogit.AppendPeeled, Timeout: 0,
		})
		if listErr != nil {
			return fmt.Errorf("ls-remote %s: %w", url, listErr)
		}
		refs = listed
		return nil
	})
	if err != nil {
		return plumbing.ZeroHash, "", err
	}
	return matchRef(refs, ref)
}

// matchRef selects the commit a ref resolves to from a remote ref listing,
// preferring a branch head, then an annotated tag's peeled commit, then a
// lightweight tag, then a fully-qualified ref given verbatim. An empty ref
// resolves the remote's HEAD — the default branch tip an unpinned source
// tracks.
func matchRef(refs []*plumbing.Reference, ref string) (plumbing.Hash, plumbing.ReferenceName, error) {
	byName := make(map[string]plumbing.Hash, len(refs))
	for _, r := range refs {
		byName[r.Name().String()] = r.Hash()
	}
	if ref == "" {
		return matchHEAD(refs, byName)
	}
	head := plumbing.NewBranchReferenceName(ref).String()
	tag := plumbing.NewTagReferenceName(ref).String()
	peeled := tag + "^{}"
	switch {
	case present(byName, head):
		return byName[head], plumbing.ReferenceName(head), nil
	case present(byName, peeled):
		return byName[peeled], plumbing.ReferenceName(tag), nil
	case present(byName, tag):
		return byName[tag], plumbing.ReferenceName(tag), nil
	case present(byName, ref):
		return byName[ref], plumbing.ReferenceName(ref), nil
	default:
		return plumbing.ZeroHash, "", fmt.Errorf("ref %q not found on remote", ref)
	}
}

// matchHEAD resolves the remote's default branch from the advertised HEAD: the
// symbolic HEAD's target branch when advertised (so the clone can check that
// branch out by name), else HEAD's own advertised commit.
func matchHEAD(refs []*plumbing.Reference, byName map[string]plumbing.Hash) (
	plumbing.Hash, plumbing.ReferenceName, error,
) {
	for _, r := range refs {
		if r.Name() != plumbing.HEAD {
			continue
		}
		if r.Type() == plumbing.SymbolicReference && present(byName, r.Target().String()) {
			return byName[r.Target().String()], r.Target(), nil
		}
		if !r.Hash().IsZero() {
			return r.Hash(), plumbing.HEAD, nil
		}
	}
	return plumbing.ZeroHash, "", errors.New(
		"remote advertises no HEAD — cannot resolve a default branch for an unpinned source",
	)
}

// present reports whether name is a non-zero entry in the ref map.
func present(byName map[string]plumbing.Hash, name string) bool {
	h, ok := byName[name]
	return ok && !h.IsZero()
}

// Clone dispatches to the sha-pinned or ref-tracking clone path.
func (f GitFetcher) Clone(ctx context.Context, opts CloneOptions) (string, error) {
	if opts.SHA != "" {
		return f.cloneSHA(ctx, opts)
	}
	return f.cloneRef(ctx, opts)
}

// cloneRef resolves the ref (the remote's default branch for an unpinned
// source), then performs a single-branch shallow clone of it with no tags,
// returning the commit the ref pointed at.
func (f GitFetcher) cloneRef(ctx context.Context, opts CloneOptions) (string, error) {
	hash, refName, err := f.resolveRefFull(ctx, opts.URL, opts.Ref)
	if err != nil {
		return "", err
	}
	displayRef := opts.Ref
	if displayRef == "" {
		displayRef = refName.String()
	}
	cloneErr := withHTTPAuthRetry(ctx, opts.URL, func(copts []client.Option) error {
		_, e := gogit.PlainCloneContext(ctx, opts.Dir, &gogit.CloneOptions{
			URL:               opts.URL,
			ClientOptions:     copts,
			RemoteName:        remoteName,
			ReferenceName:     refName,
			SingleBranch:      true,
			Mirror:            false,
			NoCheckout:        false,
			Depth:             1,
			RecurseSubmodules: gogit.NoRecurseSubmodules,
			ShallowSubmodules: false,
			Progress:          nil,
			Tags:              gogit.NoTags,
			Shared:            false,
			Filter:            "",
			Bare:              false,
			AllowEmptyRepo:    false,
		})
		if e != nil {
			return fmt.Errorf("clone %s@%s: %w", opts.URL, displayRef, e)
		}
		return nil
	})
	if cloneErr != nil {
		return "", cloneErr
	}
	return hash.String(), nil
}

// cloneSHA fetches a single commit by SHA into a fresh repository, falling back
// to a full fetch when the server rejects a want-by-SHA request, then checks the
// commit out into the worktree.
func (f GitFetcher) cloneSHA(ctx context.Context, opts CloneOptions) (string, error) {
	repo, err := gogit.PlainInit(opts.Dir, false)
	if err != nil {
		return "", fmt.Errorf("init %s: %w", opts.Dir, err)
	}
	if _, err = repo.CreateRemote(&gogitconfig.RemoteConfig{
		Name: remoteName, URLs: []string{opts.URL}, Mirror: false, Fetch: nil,
	}); err != nil {
		return "", fmt.Errorf("configure remote for %s: %w", opts.URL, err)
	}

	if fetchErr := fetchSHA(ctx, repo, opts.URL, opts.SHA); fetchErr != nil {
		return "", fetchErr
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("worktree for %s: %w", opts.Dir, err)
	}
	if coErr := wt.Checkout(&gogit.CheckoutOptions{
		Hash:                      plumbing.NewHash(opts.SHA),
		Branch:                    "",
		Create:                    false,
		Force:                     true,
		Keep:                      false,
		SparseCheckoutDirectories: nil,
	}); coErr != nil {
		return "", fmt.Errorf("checkout %s: %w", opts.SHA, coErr)
	}
	return opts.SHA, nil
}

// fetchSHA fetches the wanted commit shallowly by SHA, falling back to a full
// fetch of all heads and tags when the server rejects the want-by-SHA request.
func fetchSHA(ctx context.Context, repo *gogit.Repository, url, sha string) error {
	shallow := gogitconfig.RefSpec("+" + sha + ":" + shaFetchRef)
	shallowErr := withHTTPAuthRetry(ctx, url, func(copts []client.Option) error {
		return repo.FetchContext(ctx, fetchOptions([]gogitconfig.RefSpec{shallow}, 1, copts))
	})
	if shallowErr == nil || errors.Is(shallowErr, gogit.NoErrAlreadyUpToDate) {
		return nil
	}

	full := []gogitconfig.RefSpec{
		"+refs/heads/*:refs/remotes/origin/*",
		"+refs/tags/*:refs/tags/*",
	}
	fullErr := withHTTPAuthRetry(ctx, url, func(copts []client.Option) error {
		return repo.FetchContext(ctx, fetchOptions(full, 0, copts))
	})
	if fullErr != nil && !errors.Is(fullErr, gogit.NoErrAlreadyUpToDate) {
		return fmt.Errorf("fetch %s sha %s: %w", url, sha, fullErr)
	}
	return nil
}

// fetchOptions builds a fully-specified FetchOptions for a bundle fetch.
func fetchOptions(refSpecs []gogitconfig.RefSpec, depth int, copts []client.Option) *gogit.FetchOptions {
	return &gogit.FetchOptions{
		RemoteName:    remoteName,
		RemoteURL:     "",
		RefSpecs:      refSpecs,
		Depth:         depth,
		ClientOptions: copts,
		Progress:      nil,
		Tags:          gogit.NoTags,
		Force:         true,
		Prune:         false,
		Filter:        "",
	}
}
