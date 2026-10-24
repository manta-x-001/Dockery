package data

import (
	"context"
	"fmt"

	"api/internal/biz"
	"api/internal/data/ent"
	"api/internal/data/ent/namespace"
)

type namespaceRepo struct{ data *Data }

func NewNamespaceRepo(d *Data) biz.NamespaceRepo { return &namespaceRepo{data: d} }

func (r *namespaceRepo) Create(ctx context.Context, n *biz.Namespace) error {
	err := r.data.DB().Namespace.Create().
		SetPath(n.Path).
		SetDescription(n.Description).
		SetCreatedBy(n.CreatedBy).
		Exec(ctx)
	if ent.IsConstraintError(err) {
		return biz.ErrNamespaceExists
	}
	if err != nil {
		return fmt.Errorf("namespace create: %w", err)
	}
	return nil
}

func (r *namespaceRepo) Delete(ctx context.Context, path string) error {
	n, err := r.data.DB().Namespace.Delete().Where(namespace.PathEQ(path)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return biz.ErrNamespaceNotFound
	}
	return nil
}

func (r *namespaceRepo) List(ctx context.Context) ([]*biz.Namespace, error) {
	rows, err := r.data.DB().Namespace.Query().Order(ent.Asc(namespace.FieldPath)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.Namespace, 0, len(rows))
	for _, n := range rows {
		out = append(out, &biz.Namespace{Path: n.Path, Description: n.Description, CreatedBy: n.CreatedBy, CreatedAt: n.CreatedAt.Unix()})
	}
	return out, nil
}

var _ biz.NamespaceRepo = (*namespaceRepo)(nil)
