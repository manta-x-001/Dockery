package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ImageIndex holds one row per (repo, tag) with the git.* labels read
// from the image config at push time. It backs the reverse lookup from
// git coordinates to images and is a cache like repo_meta: the registry
// stays authoritative and rows are rebuilt by the refresh worker.
type ImageIndex struct {
	ent.Schema
}

func (ImageIndex) Fields() []ent.Field {
	return []ent.Field{
		field.String("repo").NotEmpty(),
		field.String("tag").NotEmpty(),
		field.String("digest").NotEmpty(),
		// git_repo is stored verbatim; git_repo_norm is the canonical
		// form (see biz.NormalizeGitRepo) that searches compare against.
		field.String("git_repo").Default(""),
		field.String("git_repo_norm").Default(""),
		field.String("git_commit").Default(""),
		field.String("git_branch").Default(""),
		field.String("git_tag").Default(""),
		field.String("git_hash").Default(""),
		field.Time("indexed_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (ImageIndex) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("repo", "tag").Unique(),
		index.Fields("digest"),
		index.Fields("git_repo_norm"),
		index.Fields("git_commit"),
		index.Fields("git_hash"),
		index.Fields("git_branch"),
		index.Fields("git_tag"),
	}
}
