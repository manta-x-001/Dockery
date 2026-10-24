package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Namespace is an explicitly created, possibly empty, path prefix such
// as "org" or "org/team". Registry has no notion of an empty namespace,
// so rows here only record admin intent; namespaces implied by existing
// repositories are derived from repo_meta at read time and need no row.
type Namespace struct {
	ent.Schema
}

func (Namespace) Fields() []ent.Field {
	return []ent.Field{
		// Lowercase path segments joined by "/", no leading/trailing slash.
		field.String("path").NotEmpty().Unique().Immutable(),
		field.String("description").Optional(),
		field.String("created_by").Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}
