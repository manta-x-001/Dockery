package service

import (
	_ "embed"
	"errors"
	"net/http"
	"strings"

	"api/internal/biz"
	"api/internal/util/scope"

	"github.com/bizjs/kratoscarf/response"
	"github.com/bizjs/kratoscarf/router"
)

//go:embed openapi.yaml
var openapiSpec []byte

// ImageSearchService answers "which images were built from this git
// state?" for both the UI (session cookie) and CI tooling (HTTP Basic
// with a Dockery username/password, same as docker login).
type ImageSearchService struct {
	index *biz.ImageIndexUsecase
	users *biz.UserUsecase
	perms *biz.PermissionUsecase
}

func NewImageSearchService(index *biz.ImageIndexUsecase, users *biz.UserUsecase, perms *biz.PermissionUsecase) *ImageSearchService {
	return &ImageSearchService{index: index, users: users, perms: perms}
}

type ImageSearchItem struct {
	Repo      string `json:"repo"`
	Tag       string `json:"tag"`
	Image     string `json:"image"` // repo:tag
	Digest    string `json:"digest"`
	GitRepo   string `json:"git_repo,omitempty"`
	GitCommit string `json:"git_commit,omitempty"`
	GitBranch string `json:"git_branch,omitempty"`
	GitTag    string `json:"git_tag,omitempty"`
	GitHash   string `json:"git_hash,omitempty"`
	IndexedAt int64  `json:"indexed_at"`
}

type ImageSearchResponse struct {
	Items    []ImageSearchItem `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"page_size"`
}

// Spec serves the OpenAPI document. Public: it describes the API, not data.
func (s *ImageSearchService) Spec(ctx *router.Context) error {
	return ctx.Data(http.StatusOK, "application/yaml; charset=utf-8", openapiSpec)
}

// authenticate accepts a session or HTTP Basic credentials.
func (s *ImageSearchService) authenticate(ctx *router.Context) (*biz.User, error) {
	if id := sessionUserID(ctx); id != 0 {
		if u, err := s.users.GetByID(ctx.Context(), id); err == nil {
			return u, nil
		}
		return nil, response.ErrUnauthorized
	}
	name, pass, ok := parseBasicAuth(ctx.Header("Authorization"))
	if !ok {
		ctx.SetHeader("WWW-Authenticate", `Basic realm="dockery"`)
		return nil, response.ErrUnauthorized
	}
	u, err := s.users.VerifyCredentials(ctx.Context(), name, pass)
	if err != nil {
		ctx.SetHeader("WWW-Authenticate", `Basic realm="dockery"`)
		return nil, response.ErrUnauthorized
	}
	return u, nil
}

// query reads a filter given as either "git.repo" or "git_repo".
func query(ctx *router.Context, dotted string) string {
	if v := ctx.Query(dotted); v != "" {
		return v
	}
	return ctx.Query(strings.ReplaceAll(dotted, ".", "_"))
}

// Search is GET /api/images/search.
func (s *ImageSearchService) Search(ctx *router.Context) error {
	user, err := s.authenticate(ctx)
	if err != nil {
		return err
	}
	page := clampNonNeg(queryIntDefault(ctx, "page", 0))
	pageSize := queryIntDefault(ctx, "page_size", 50)
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
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

	f := biz.ImageSearchFilter{
		GitRepo:   query(ctx, "git.repo"),
		GitCommit: query(ctx, "git.commit"),
		GitHash:   query(ctx, "git.hash"),
		GitBranch: query(ctx, "git.branch"),
		GitTag:    query(ctx, "git.tag"),
		Digest:    ctx.Query("digest"),
		Fuzzy:     ctx.Query("fuzzy") == "true",
	}
	restricted := len(patterns) > 0
	if !restricted {
		f.Limit, f.Offset = pageSize, page*pageSize
	}
	rows, total, err := s.index.Search(ctx.Context(), f)
	if err != nil {
		if errors.Is(err, biz.ErrSearchTooBroad) {
			return response.NewBizError(http.StatusBadRequest, 40000,
				"provide at least one of git.repo, git.commit, git.hash, git.branch, git.tag, digest")
		}
		return response.ErrBadRequest.WithMessage(err.Error())
	}
	if restricted {
		// Glob patterns can't be pushed into SQL; the filters above keep
		// the candidate set small, so filter and page here.
		kept := rows[:0]
		for _, r := range rows {
			for _, p := range patterns {
				if scope.MatchPattern(p, r.Repo) {
					kept = append(kept, r)
					break
				}
			}
		}
		total = len(kept)
		start := min(page*pageSize, total)
		rows = kept[start:min(start+pageSize, total)]
	}

	out := ImageSearchResponse{Items: make([]ImageSearchItem, 0, len(rows)), Total: total, Page: page, PageSize: pageSize}
	for _, r := range rows {
		out.Items = append(out.Items, ImageSearchItem{
			Repo: r.Repo, Tag: r.Tag, Image: r.Repo + ":" + r.Tag, Digest: r.Digest,
			GitRepo: r.GitRepo, GitCommit: r.GitCommit, GitBranch: r.GitBranch,
			GitTag: r.GitTag, GitHash: r.GitHash, IndexedAt: r.IndexedAt,
		})
	}
	return ctx.Success(out)
}
