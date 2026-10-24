package data

import (
	"context"
	"fmt"

	"api/internal/biz"
	"api/internal/data/ent"
	"api/internal/data/ent/imageindex"
)

type imageIndexRepo struct{ data *Data }

func NewImageIndexRepo(d *Data) biz.ImageIndexRepo { return &imageIndexRepo{data: d} }

// Upsert follows repo_meta's pattern: Create, and on a unique-index
// collision on (repo, tag) update that row in place.
func (r *imageIndexRepo) Upsert(ctx context.Context, rec *biz.ImageRecord) error {
	norm := biz.NormalizeGitRepo(rec.GitRepo)
	err := r.data.DB().ImageIndex.Create().
		SetRepo(rec.Repo).SetTag(rec.Tag).SetDigest(rec.Digest).
		SetGitRepo(rec.GitRepo).SetGitRepoNorm(norm).
		SetGitCommit(rec.GitCommit).SetGitBranch(rec.GitBranch).
		SetGitTag(rec.GitTag).SetGitHash(rec.GitHash).
		Exec(ctx)
	if err == nil {
		return nil
	}
	if !ent.IsConstraintError(err) {
		return fmt.Errorf("image_index create: %w", err)
	}
	_, err = r.data.DB().ImageIndex.Update().
		Where(imageindex.RepoEQ(rec.Repo), imageindex.TagEQ(rec.Tag)).
		SetDigest(rec.Digest).
		SetGitRepo(rec.GitRepo).SetGitRepoNorm(norm).
		SetGitCommit(rec.GitCommit).SetGitBranch(rec.GitBranch).
		SetGitTag(rec.GitTag).SetGitHash(rec.GitHash).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("image_index update: %w", err)
	}
	return nil
}

func (r *imageIndexRepo) Tags(ctx context.Context, repo string) ([]string, error) {
	return r.data.DB().ImageIndex.Query().
		Where(imageindex.RepoEQ(repo)).
		Select(imageindex.FieldTag).Strings(ctx)
}

func (r *imageIndexRepo) DeleteTag(ctx context.Context, repo, tag string) error {
	_, err := r.data.DB().ImageIndex.Delete().
		Where(imageindex.RepoEQ(repo), imageindex.TagEQ(tag)).Exec(ctx)
	return err
}

func (r *imageIndexRepo) DeleteRepo(ctx context.Context, repo string) error {
	_, err := r.data.DB().ImageIndex.Delete().Where(imageindex.RepoEQ(repo)).Exec(ctx)
	return err
}

func (r *imageIndexRepo) Search(ctx context.Context, f biz.ImageSearchFilter) ([]*biz.ImageRecord, int, error) {
	q := r.data.DB().ImageIndex.Query()
	if f.GitRepo != "" {
		if f.Fuzzy {
			q = q.Where(imageindex.GitRepoNormContains(biz.NormalizeGitRepo(f.GitRepo)))
		} else {
			q = q.Where(imageindex.GitRepoNormEQ(biz.NormalizeGitRepo(f.GitRepo)))
		}
	}
	if f.GitCommit != "" {
		q = q.Where(imageindex.GitCommitHasPrefix(f.GitCommit))
	}
	if f.GitHash != "" {
		q = q.Where(imageindex.GitHashHasPrefix(f.GitHash))
	}
	if f.GitBranch != "" {
		if f.Fuzzy {
			q = q.Where(imageindex.GitBranchContainsFold(f.GitBranch))
		} else {
			q = q.Where(imageindex.GitBranchEQ(f.GitBranch))
		}
	}
	if f.GitTag != "" {
		if f.Fuzzy {
			q = q.Where(imageindex.GitTagContainsFold(f.GitTag))
		} else {
			q = q.Where(imageindex.GitTagEQ(f.GitTag))
		}
	}
	if f.Digest != "" {
		q = q.Where(imageindex.DigestEQ(f.Digest))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	q = q.Order(ent.Desc(imageindex.FieldIndexedAt), ent.Asc(imageindex.FieldRepo), ent.Asc(imageindex.FieldTag))
	if f.Offset > 0 {
		q = q.Offset(f.Offset)
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*biz.ImageRecord, 0, len(rows))
	for _, x := range rows {
		out = append(out, &biz.ImageRecord{
			Repo: x.Repo, Tag: x.Tag, Digest: x.Digest,
			GitRepo: x.GitRepo, GitCommit: x.GitCommit, GitBranch: x.GitBranch,
			GitTag: x.GitTag, GitHash: x.GitHash, IndexedAt: x.IndexedAt.Unix(),
		})
	}
	return out, total, nil
}

var _ biz.ImageIndexRepo = (*imageIndexRepo)(nil)
