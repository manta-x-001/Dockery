package biz

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"api/internal/util/registryfetch"

	"github.com/go-kratos/kratos/v2/log"
)

// Label keys read from the image config at push time.
const (
	LabelGitRepo   = "git.repo"
	LabelGitCommit = "git.commit"
	LabelGitBranch = "git.branch"
	LabelGitTag    = "git.tag"
	LabelGitHash   = "git.hash"
)

// ImageRecord is one indexed (repo, tag) with its git coordinates.
type ImageRecord struct {
	Repo      string
	Tag       string
	Digest    string
	GitRepo   string
	GitCommit string
	GitBranch string
	GitTag    string
	GitHash   string
	IndexedAt int64
}

// ImageSearchFilter selects rows from the index. Empty fields are not
// constrained; all non-empty fields are AND-ed.
type ImageSearchFilter struct {
	// GitRepo matches after NormalizeGitRepo, so URL spellings of the
	// same repository are equivalent.
	GitRepo string
	// GitCommit / GitHash match as a prefix (callers enforce a minimum
	// length) so abbreviated hashes work.
	GitCommit string
	GitHash   string
	GitBranch string
	GitTag    string
	Digest    string
	// Fuzzy switches git.repo / git.branch / git.tag from exact to
	// case-insensitive substring matching (keyword search in the UI).
	Fuzzy bool
	// Limit caps the rows returned; Offset skips from the start.
	Limit  int
	Offset int
}

// ImageIndexRepo is the data-layer contract, implemented in
// internal/data/image_index.go.
type ImageIndexRepo interface {
	Upsert(ctx context.Context, r *ImageRecord) error
	// Tags lists the indexed tags of a repo.
	Tags(ctx context.Context, repo string) ([]string, error)
	DeleteTag(ctx context.Context, repo, tag string) error
	DeleteRepo(ctx context.Context, repo string) error
	// Search returns matching rows ordered newest first plus the total
	// count ignoring Limit/Offset.
	Search(ctx context.Context, f ImageSearchFilter) ([]*ImageRecord, int, error)
}

// ImageIndexUsecase extracts git.* labels from pushed images and serves
// reverse lookups. Two write paths feed it:
//
//   - push events carry a tag → IndexTagAsync re-reads that tag (a tag
//     may have been moved to a new image)
//   - the repo refresh worker calls SyncRepo → indexes tags never seen
//     before and drops rows for tags that no longer exist, which also
//     backfills images pushed before this feature existed
type ImageIndexUsecase struct {
	repo     ImageIndexRepo
	registry *registryfetch.Client
	logger   *log.Helper
	sem      chan struct{}
}

// maxConcurrentIndex bounds in-flight tag indexing goroutines so a burst
// of pushes cannot fan out unbounded requests at the registry.
const maxConcurrentIndex = 4

func NewImageIndexUsecase(repo ImageIndexRepo, registry *registryfetch.Client, meta *RepoMetaUsecase, logger log.Logger) *ImageIndexUsecase {
	u := &ImageIndexUsecase{
		repo:     repo,
		registry: registry,
		logger:   log.NewHelper(log.With(logger, "module", "biz/image_index")),
		sem:      make(chan struct{}, maxConcurrentIndex),
	}
	meta.AddRefreshHook(u.SyncRepo)
	return u
}

// IndexTagAsync indexes repo:tag in the background.
func (u *ImageIndexUsecase) IndexTagAsync(repo, tag string) {
	if repo == "" || tag == "" {
		return
	}
	go func() {
		u.sem <- struct{}{}
		defer func() { <-u.sem }()
		if err := u.IndexTag(context.Background(), repo, tag); err != nil {
			u.logger.Warnf("index %s:%s: %v", repo, tag, err)
		}
	}()
}

// IndexTag reads repo:tag's manifest and config and upserts its row.
func (u *ImageIndexUsecase) IndexTag(ctx context.Context, repo, tag string) error {
	m, digest, err := u.registry.ManifestWithDigest(ctx, repo, tag)
	if err != nil {
		return err
	}
	labels, err := u.labelsFor(ctx, repo, m)
	if err != nil {
		return err
	}
	rec := &ImageRecord{
		Repo:      repo,
		Tag:       tag,
		Digest:    digest,
		GitRepo:   strings.TrimSpace(labels[LabelGitRepo]),
		GitCommit: strings.ToLower(strings.TrimSpace(labels[LabelGitCommit])),
		GitBranch: strings.TrimSpace(labels[LabelGitBranch]),
		GitTag:    strings.TrimSpace(labels[LabelGitTag]),
		GitHash:   strings.ToLower(strings.TrimSpace(labels[LabelGitHash])),
	}
	return u.repo.Upsert(ctx, rec)
}

// labelsFor returns the image labels for a manifest. For an image index
// it takes the first real platform (skipping BuildKit's unknown/unknown
// attestations) whose config carries any git.* label, since all
// platforms of one build share the same labels.
func (u *ImageIndexUsecase) labelsFor(ctx context.Context, repo string, m *registryfetch.Manifest) (map[string]string, error) {
	if !m.IsList() {
		return u.configLabels(ctx, repo, m)
	}
	var firstErr error
	for _, e := range m.Manifests {
		if e.Platform.Os == "unknown" || e.Digest == "" {
			continue
		}
		child, err := u.registry.Manifest(ctx, repo, e.Digest)
		if err == nil {
			var labels map[string]string
			if labels, err = u.configLabels(ctx, repo, child); err == nil && hasGitLabel(labels) {
				return labels, nil
			}
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// A failure with nothing found must not be recorded as "no labels":
	// the row would then never be retried.
	return nil, firstErr
}

func (u *ImageIndexUsecase) configLabels(ctx context.Context, repo string, m *registryfetch.Manifest) (map[string]string, error) {
	if m.Config == nil || m.Config.Digest == "" {
		return nil, nil
	}
	cfg, err := u.registry.ConfigBlob(ctx, repo, m.Config.Digest)
	if err != nil {
		return nil, err
	}
	return cfg.ImageLabels(), nil
}

func hasGitLabel(l map[string]string) bool {
	for _, k := range []string{LabelGitRepo, LabelGitCommit, LabelGitBranch, LabelGitTag, LabelGitHash} {
		if l[k] != "" {
			return true
		}
	}
	return false
}

// SyncRepo reconciles the index with the repo's current tag list.
func (u *ImageIndexUsecase) SyncRepo(ctx context.Context, repo string, tags []string) {
	u.syncRepo(ctx, repo, tags, false)
}

// syncRepo is SyncRepo with an optional force flag: when set, tags that
// are already indexed are re-read too (used by Rebuild to repair rows
// whose tag was re-pushed while a webhook was missed).
func (u *ImageIndexUsecase) syncRepo(ctx context.Context, repo string, tags []string, force bool) {
	if len(tags) == 0 {
		if err := u.repo.DeleteRepo(ctx, repo); err != nil {
			u.logger.Warnf("drop index %s: %v", repo, err)
		}
		return
	}
	indexed, err := u.repo.Tags(ctx, repo)
	if err != nil {
		u.logger.Warnf("list indexed tags %s: %v", repo, err)
		return
	}
	have := make(map[string]struct{}, len(indexed))
	for _, t := range indexed {
		have[t] = struct{}{}
	}
	live := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		live[t] = struct{}{}
		if _, ok := have[t]; ok && !force {
			continue
		}
		if err := u.IndexTag(ctx, repo, t); err != nil {
			u.logger.Warnf("index %s:%s: %v", repo, t, err)
		}
	}
	for _, t := range indexed {
		if _, ok := live[t]; !ok {
			if err := u.repo.DeleteTag(ctx, repo, t); err != nil {
				u.logger.Warnf("drop index %s:%s: %v", repo, t, err)
			}
		}
	}
}

// Rebuild re-derives the whole index from the registry itself: it walks
// /v2/_catalog and re-reads every tag's manifest and config. The index
// is only a cache of what is in registry storage, so this recovers it
// after the SQLite file is lost or the schema changed. Returns the
// number of repos visited. Safe to run repeatedly.
func (u *ImageIndexUsecase) Rebuild(ctx context.Context) (int, error) {
	n, cursor := 0, ""
	for {
		repos, next, err := u.registry.Catalog(ctx, cursor, 100)
		if err != nil {
			return n, err
		}
		for _, repo := range repos {
			tags, err := u.registry.Tags(ctx, repo)
			if err != nil {
				u.logger.Warnf("rebuild: list tags %s: %v", repo, err)
				continue
			}
			u.syncRepo(ctx, repo, tags, true)
			n++
		}
		if next == "" || len(repos) == 0 {
			return n, nil
		}
		cursor = next
	}
}

// ErrSearchTooBroad is returned when a search has no usable filter.
var ErrSearchTooBroad = errors.New("image index: at least one filter is required")

// minHashPrefix is the shortest commit/hash prefix accepted. Shorter
// prefixes match far too many rows to be a meaningful lookup.
const minHashPrefix = 7

// Search runs a reverse lookup. Filters are validated here so every
// caller gets the same rules.
func (u *ImageIndexUsecase) Search(ctx context.Context, f ImageSearchFilter) ([]*ImageRecord, int, error) {
	f.GitRepo = strings.TrimSpace(f.GitRepo)
	f.GitCommit = strings.ToLower(strings.TrimSpace(f.GitCommit))
	f.GitHash = strings.ToLower(strings.TrimSpace(f.GitHash))
	if f.GitRepo == "" && f.GitCommit == "" && f.GitHash == "" &&
		f.GitBranch == "" && f.GitTag == "" && f.Digest == "" {
		return nil, 0, ErrSearchTooBroad
	}
	for name, v := range map[string]string{"git.commit": f.GitCommit, "git.hash": f.GitHash} {
		if v != "" && len(v) < minHashPrefix {
			return nil, 0, fmt.Errorf("%s needs at least %d characters", name, minHashPrefix)
		}
	}
	return u.repo.Search(ctx, f)
}

// NormalizeGitRepo reduces the common spellings of a repository URL to
// one comparable form: lowercase host/path with no scheme, credentials,
// trailing slash or ".git". So https://github.com/Org/App.git,
// git@github.com:org/app and github.com/org/app all compare equal.
func NormalizeGitRepo(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Drop credentials / the "git@" user. The user part ends at the
	// first "@" that precedes any "/".
	if at := strings.Index(s, "@"); at >= 0 && !strings.Contains(s[:at], "/") {
		s = s[at+1:]
	}
	// scp-like "host:org/app" → "host/org/app". A port ("host:22/…")
	// is kept by only rewriting when the part after ":" isn't numeric.
	if c := strings.Index(s, ":"); c >= 0 && !strings.Contains(s[:c], "/") {
		rest := s[c+1:]
		port, _, _ := strings.Cut(rest, "/")
		if _, err := strconv.Atoi(port); err != nil {
			s = s[:c] + "/" + rest
		}
	}
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.TrimRight(s, "/")
}
