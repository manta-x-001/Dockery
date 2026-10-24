package service

import (
	"errors"
	"strconv"
	"strings"

	"api/internal/biz"

	"github.com/bizjs/kratoscarf/response"
	"github.com/bizjs/kratoscarf/router"
)

// NamespaceService exposes the namespace tree and namespace-level
// grants. A grant is a repo_permissions row with pattern "<ns>/**", so
// both the UI catalog filter and the docker token realm honour it with
// no extra code path.
type NamespaceService struct {
	ns    *biz.NamespaceUsecase
	perms *biz.PermissionUsecase
	users *biz.UserUsecase
	audit *biz.AuditUsecase
}

func NewNamespaceService(ns *biz.NamespaceUsecase, perms *biz.PermissionUsecase, users *biz.UserUsecase, audit *biz.AuditUsecase) *NamespaceService {
	return &NamespaceService{ns: ns, perms: perms, users: users, audit: audit}
}

type NamespaceView struct {
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	Explicit    bool   `json:"explicit"`
	RepoCount   int    `json:"repo_count"`
}

type NamespaceListView struct {
	Items []NamespaceView `json:"items"`
}

type CreateNamespaceRequest struct {
	Path        string `json:"path" validate:"required,min=1,max=200"`
	Description string `json:"description" validate:"max=500"`
}

type NamespaceGrantView struct {
	PermissionID int    `json:"permission_id"`
	UserID       int    `json:"user_id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
}

type NamespaceGrantListView struct {
	Items []NamespaceGrantView `json:"items"`
}

type NamespaceGrantRequest struct {
	Path   string `json:"path" validate:"required,min=1,max=200"`
	UserID int    `json:"user_id" validate:"required,min=1"`
}

// List returns the tree visible to the session user.
func (s *NamespaceService) List(ctx *router.Context) error {
	user, err := s.users.GetByID(ctx.Context(), sessionUserID(ctx))
	if err != nil {
		return response.ErrUnauthorized
	}
	var patterns []string
	if user.Role != "admin" {
		perms, err := s.perms.ListForUser(ctx.Context(), user.ID)
		if err != nil {
			return response.ErrInternal.WithCause(err)
		}
		for _, p := range perms {
			patterns = append(patterns, p.RepoPattern)
		}
	}
	// nil = unrestricted: admins and users without patterns.
	nodes, err := s.ns.Tree(ctx.Context(), patterns)
	if err != nil {
		return response.ErrInternal.WithCause(err)
	}
	out := NamespaceListView{Items: make([]NamespaceView, 0, len(nodes))}
	for _, n := range nodes {
		out.Items = append(out.Items, NamespaceView{
			Path: n.Path, Description: n.Description, Explicit: n.Explicit, RepoCount: n.RepoCount,
		})
	}
	return ctx.Success(out)
}

func (s *NamespaceService) Create(ctx *router.Context) error {
	var req CreateNamespaceRequest
	if err := ctx.Bind(&req); err != nil {
		return err
	}
	path := strings.Trim(strings.TrimSpace(req.Path), "/")
	if err := s.ns.Create(ctx.Context(), path, req.Description, sessionUsername(ctx)); err != nil {
		return nsError(err)
	}
	s.auditNS(ctx, biz.ActionNamespaceCreated, path, nil)
	return ctx.Success(NamespaceView{Path: path, Description: req.Description, Explicit: true})
}

func (s *NamespaceService) Delete(ctx *router.Context) error {
	path := strings.Trim(ctx.Query("path"), "/")
	if err := s.ns.Delete(ctx.Context(), path); err != nil {
		return nsError(err)
	}
	s.auditNS(ctx, biz.ActionNamespaceDeleted, path, nil)
	return ctx.Success(nil)
}

func (s *NamespaceService) ListGrants(ctx *router.Context) error {
	path := strings.Trim(ctx.Query("path"), "/")
	if err := biz.ValidateNamespacePath(path); err != nil {
		return nsError(err)
	}
	rows, err := s.perms.ListByPattern(ctx.Context(), biz.GrantPattern(path))
	if err != nil {
		return response.ErrInternal.WithCause(err)
	}
	out := NamespaceGrantListView{Items: make([]NamespaceGrantView, 0, len(rows))}
	for _, p := range rows {
		u, err := s.users.GetByID(ctx.Context(), p.UserID)
		if err != nil {
			continue
		}
		out.Items = append(out.Items, NamespaceGrantView{
			PermissionID: p.ID, UserID: u.ID, Username: u.Username, Role: u.Role,
		})
	}
	return ctx.Success(out)
}

func (s *NamespaceService) Grant(ctx *router.Context) error {
	var req NamespaceGrantRequest
	if err := ctx.Bind(&req); err != nil {
		return err
	}
	path := strings.Trim(req.Path, "/")
	if err := biz.ValidateNamespacePath(path); err != nil {
		return nsError(err)
	}
	if _, err := s.perms.GrantPatterns(ctx.Context(), req.UserID, []string{biz.GrantPattern(path)}); err != nil {
		switch {
		case errors.Is(err, biz.ErrUserNotFound):
			return response.ErrNotFound
		case errors.Is(err, biz.ErrAdminNeedsNoPerm):
			return response.ErrBadRequest.WithMessage("admins already have access to every namespace")
		}
		return response.ErrInternal.WithCause(err)
	}
	s.auditNS(ctx, biz.ActionNamespaceGranted, path, map[string]any{"user_id": req.UserID})
	return ctx.Success(nil)
}

func (s *NamespaceService) Revoke(ctx *router.Context) error {
	path := strings.Trim(ctx.Query("path"), "/")
	uid, err := strconv.Atoi(ctx.Query("user_id"))
	if err != nil || uid <= 0 {
		return response.ErrBadRequest.WithMessage("invalid user_id")
	}
	if err := biz.ValidateNamespacePath(path); err != nil {
		return nsError(err)
	}
	rows, err := s.perms.ListByPattern(ctx.Context(), biz.GrantPattern(path))
	if err != nil {
		return response.ErrInternal.WithCause(err)
	}
	for _, p := range rows {
		if p.UserID == uid {
			if err := s.perms.Revoke(ctx.Context(), p.ID); err != nil && !errors.Is(err, biz.ErrPermissionNotFound) {
				return response.ErrInternal.WithCause(err)
			}
		}
	}
	s.auditNS(ctx, biz.ActionNamespaceRevoked, path, map[string]any{"user_id": uid})
	return ctx.Success(nil)
}

func (s *NamespaceService) auditNS(ctx *router.Context, action, path string, detail map[string]any) {
	s.audit.Write(ctx.Context(), biz.AuditEntry{
		Actor:    sessionUsername(ctx),
		Action:   action,
		Target:   "namespace:" + path,
		ClientIP: ctx.ClientIP(),
		Success:  true,
		Detail:   detail,
	})
}

func nsError(err error) error {
	switch {
	case errors.Is(err, biz.ErrNamespaceInvalid):
		return response.ErrBadRequest.WithMessage("invalid namespace path: use lowercase segments like org/team")
	case errors.Is(err, biz.ErrNamespaceExists):
		return response.ErrConflict.WithMessage("namespace already exists")
	case errors.Is(err, biz.ErrNamespaceNotFound):
		return response.ErrNotFound
	case errors.Is(err, biz.ErrNamespaceNotEmpty):
		return response.ErrConflict.WithMessage("namespace still has repositories or child namespaces")
	}
	return response.ErrInternal.WithCause(err)
}
