package biz

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"

	"api/internal/util/scope"
)

// Namespace is an explicitly created path prefix.
type Namespace struct {
	Path        string
	Description string
	CreatedBy   string
	CreatedAt   int64
}

// NamespaceRepo is the data-layer contract, implemented in
// internal/data/namespace.go.
type NamespaceRepo interface {
	Create(ctx context.Context, n *Namespace) error
	Delete(ctx context.Context, path string) error
	List(ctx context.Context) ([]*Namespace, error)
}

var (
	ErrNamespaceInvalid  = errors.New("namespace: invalid path")
	ErrNamespaceExists   = errors.New("namespace: already exists")
	ErrNamespaceNotFound = errors.New("namespace: not found")
	ErrNamespaceNotEmpty = errors.New("namespace: has repositories or child namespaces")
)

// segmentRe is the distribution path-component grammar.
var segmentRe = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)

const (
	maxNamespaceDepth = 6
	maxNamespaceLen   = 200
)

// ValidateNamespacePath reports whether p is a well-formed namespace.
func ValidateNamespacePath(p string) error {
	if p == "" || len(p) > maxNamespaceLen {
		return ErrNamespaceInvalid
	}
	segs := strings.Split(p, "/")
	if len(segs) > maxNamespaceDepth {
		return ErrNamespaceInvalid
	}
	for _, s := range segs {
		if !segmentRe.MatchString(s) {
			return ErrNamespaceInvalid
		}
	}
	return nil
}

// GrantPattern is the repo_permissions pattern that gives a user access
// to everything under namespace path at any depth.
func GrantPattern(path string) string { return path + "/**" }

// NamespaceNode is one entry of the flattened namespace tree.
type NamespaceNode struct {
	Path        string
	Description string
	// Explicit is true when an admin created the namespace; false when
	// it exists only because repositories live beneath it.
	Explicit bool
	// RepoCount counts the visible repositories at any depth below.
	RepoCount int
}

// NamespaceUsecase manages namespaces. It owns no registry data: the
// implied part of the tree is derived from the repo_meta cache.
type NamespaceUsecase struct {
	repo  NamespaceRepo
	meta  *RepoMetaUsecase
	perms *PermissionUsecase
}

func NewNamespaceUsecase(repo NamespaceRepo, meta *RepoMetaUsecase, perms *PermissionUsecase) *NamespaceUsecase {
	return &NamespaceUsecase{repo: repo, meta: meta, perms: perms}
}

// Create records an explicit namespace. Missing ancestors are created
// too so the tree never has an explicit node under an unlisted parent.
func (u *NamespaceUsecase) Create(ctx context.Context, path, desc, actor string) error {
	if err := ValidateNamespacePath(path); err != nil {
		return err
	}
	existing, err := u.repo.List(ctx)
	if err != nil {
		return err
	}
	have := make(map[string]bool, len(existing))
	for _, n := range existing {
		have[n.Path] = true
	}
	if have[path] {
		return ErrNamespaceExists
	}
	segs := strings.Split(path, "/")
	for i := 1; i <= len(segs); i++ {
		p := strings.Join(segs[:i], "/")
		if have[p] {
			continue
		}
		n := &Namespace{Path: p, CreatedBy: actor}
		if p == path {
			n.Description = desc
		}
		if err := u.repo.Create(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes an explicit namespace that has no repositories and no
// child namespaces, along with the grants made on it.
func (u *NamespaceUsecase) Delete(ctx context.Context, path string) error {
	all, err := u.repo.List(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, n := range all {
		if n.Path == path {
			found = true
		} else if strings.HasPrefix(n.Path, path+"/") {
			return ErrNamespaceNotEmpty
		}
	}
	if !found {
		return ErrNamespaceNotFound
	}
	repos, err := u.meta.AllRepos(ctx)
	if err != nil {
		return err
	}
	for _, r := range repos {
		if strings.HasPrefix(r, path+"/") {
			return ErrNamespaceNotEmpty
		}
	}
	grants, err := u.perms.ListByPattern(ctx, GrantPattern(path))
	if err != nil {
		return err
	}
	for _, g := range grants {
		if err := u.perms.Revoke(ctx, g.ID); err != nil && !errors.Is(err, ErrPermissionNotFound) {
			return err
		}
	}
	return u.repo.Delete(ctx, path)
}

// Tree returns the flattened tree sorted by path. patterns restricts a
// non-admin caller: nil means unrestricted (admin, or a user with no
// patterns), otherwise only namespaces on the way to, or inside, a
// permitted pattern are returned and repo counts include only permitted
// repositories.
func (u *NamespaceUsecase) Tree(ctx context.Context, patterns []string) ([]NamespaceNode, error) {
	explicit, err := u.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	repos, err := u.meta.AllRepos(ctx)
	if err != nil {
		return nil, err
	}
	nodes := map[string]*NamespaceNode{}
	node := func(p string) *NamespaceNode {
		n := nodes[p]
		if n == nil {
			n = &NamespaceNode{Path: p}
			nodes[p] = n
		}
		return n
	}
	for _, r := range repos {
		if patterns != nil && !matchesAny(patterns, r) {
			continue
		}
		segs := strings.Split(r, "/")
		for i := 1; i < len(segs); i++ {
			node(strings.Join(segs[:i], "/")).RepoCount++
		}
	}
	for _, e := range explicit {
		if patterns != nil && !patternsTouch(patterns, e.Path) {
			continue
		}
		n := node(e.Path)
		n.Explicit = true
		n.Description = e.Description
	}
	out := make([]NamespaceNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func matchesAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if scope.MatchPattern(p, name) {
			return true
		}
	}
	return false
}

// patternsTouch reports whether ns is an ancestor of, equal to, or
// inside the literal prefix of any pattern.
func patternsTouch(patterns []string, ns string) bool {
	for _, p := range patterns {
		base := p
		if i := strings.Index(p, "*"); i >= 0 {
			base = strings.TrimSuffix(p[:i], "/")
			if base == "" {
				return true
			}
		} else if i := strings.LastIndex(p, "/"); i >= 0 {
			base = p[:i]
		} else {
			continue
		}
		if ns == base || strings.HasPrefix(ns, base+"/") || strings.HasPrefix(base, ns+"/") {
			return true
		}
	}
	return false
}
