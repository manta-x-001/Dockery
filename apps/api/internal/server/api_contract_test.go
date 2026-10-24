package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- helpers -------------------------------------------------------------

// session is an independent cookie jar against the harness server, so one
// test can talk as anonymous, viewer and admin side by side.
type apiSession struct {
	h *harness
	c *http.Client
}

func (h *harness) session() *apiSession {
	jar, _ := cookiejar.New(nil)
	return &apiSession{h: h, c: &http.Client{Jar: jar, Timeout: 5 * time.Second}}
}

func (s *apiSession) do(method, path string, body any) (*http.Response, []byte) {
	s.h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, s.h.baseURL+path, rdr)
	if err != nil {
		s.h.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.c.Do(req)
	if err != nil {
		s.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func (s *apiSession) login(user, pass string) *apiSession {
	s.h.t.Helper()
	s.do(http.MethodGet, "/api/auth/me", nil) // primes the session cookie, as the SPA does
	if resp, raw := s.do(http.MethodPost, "/api/auth/login", map[string]string{"username": user, "password": pass}); resp.StatusCode != 200 {
		s.h.t.Fatalf("login %s: %d %s", user, resp.StatusCode, raw)
	}
	return s
}

func dataOf[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var env struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v; %s", err, raw)
	}
	var out T
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &out); err != nil {
			t.Fatalf("decode data: %v; %s", err, raw)
		}
	}
	return out
}

func wantStatus(t *testing.T, what string, resp *http.Response, raw []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("%s: want %d, got %d; body=%s", what, want, resp.StatusCode, raw)
	}
	if want >= 400 {
		var env struct{ Code int }
		_ = json.Unmarshal(raw, &env)
		if env.Code == 0 {
			t.Fatalf("%s: error response must carry a non-zero business code; body=%s", what, raw)
		}
	}
}

const adminPass = "a-strong-password-42"

func adminHarness(t *testing.T, upstream string) *harness {
	t.Helper()
	h := newHarnessWithUpstream(t, upstream)
	if err := h.users.EnsureAdmin(context.Background(), "admin", adminPass); err != nil {
		t.Fatal(err)
	}
	return h
}

// --- authorization matrix -------------------------------------------------

// Every admin-only endpoint added for namespaces, backups and the image
// index: anonymous → 401, authenticated non-admin → 403, admin → neither.
func TestAdminOnlyEndpoints_AuthMatrix(t *testing.T) {
	h := adminHarness(t, "http://127.0.0.1:1")
	defer h.stop()
	if _, err := h.users.Create(context.Background(), "vera", "viewer-password-1", "view"); err != nil {
		t.Fatal(err)
	}
	anon := h.session()
	viewer := h.session().login("vera", "viewer-password-1")
	admin := h.session().login("admin", adminPass)

	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/namespaces", map[string]any{"path": "matrix/ns"}},
		{http.MethodDelete, "/api/namespaces?path=matrix/ns", nil},
		{http.MethodGet, "/api/namespaces/grants?path=matrix", nil},
		{http.MethodPost, "/api/namespaces/grants", map[string]any{"path": "matrix", "user_id": 99999}},
		{http.MethodDelete, "/api/namespaces/grants?path=matrix&user_id=99999", nil},
		{http.MethodPost, "/api/admin/backups", nil},
		{http.MethodGet, "/api/admin/backups", nil},
		{http.MethodPost, "/api/admin/backups/restore", map[string]any{}},
		{http.MethodPost, "/api/admin/image-index/rebuild", nil},
	}
	for _, tc := range cases {
		name := tc.method + " " + tc.path
		t.Run(name, func(t *testing.T) {
			resp, raw := anon.do(tc.method, tc.path, tc.body)
			wantStatus(t, name+" anonymous", resp, raw, http.StatusUnauthorized)
			resp, raw = viewer.do(tc.method, tc.path, tc.body)
			wantStatus(t, name+" viewer", resp, raw, http.StatusForbidden)
			resp, raw = admin.do(tc.method, tc.path, tc.body)
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				t.Fatalf("%s admin was rejected: %d %s", name, resp.StatusCode, raw)
			}
		})
	}
}

// --- namespaces ----------------------------------------------------------

func TestNamespaceAPI_ValidationAndGrants(t *testing.T) {
	h := adminHarness(t, "http://127.0.0.1:1")
	defer h.stop()
	ctx := context.Background()
	admin := h.session().login("admin", adminPass)
	bob, _ := h.users.Create(ctx, "bob", "bob-password-1234", "write")

	// Missing path is caught by request validation (422); malformed ones by
	// the namespace rules (400).
	resp0, raw0 := admin.do(http.MethodPost, "/api/namespaces", map[string]any{"path": ""})
	wantStatus(t, "create empty path", resp0, raw0, http.StatusUnprocessableEntity)
	resp0, raw0 = admin.do(http.MethodPost, "/api/namespaces", map[string]any{"path": strings.Repeat("x", 300)})
	wantStatus(t, "create over-long path", resp0, raw0, http.StatusUnprocessableEntity)
	invalid := []string{"UPPER", "a b", "a//b", "../etc", "-lead", "a/b/c/d/e/f/g"}
	for _, p := range invalid {
		resp, raw := admin.do(http.MethodPost, "/api/namespaces", map[string]any{"path": p})
		wantStatus(t, fmt.Sprintf("create %q", p), resp, raw, http.StatusBadRequest)
	}

	// Grants: unknown user → 404, admin target → 400, bad user_id → 400.
	resp, raw := admin.do(http.MethodPost, "/api/namespaces/grants", map[string]any{"path": "acme", "user_id": 424242})
	wantStatus(t, "grant unknown user", resp, raw, http.StatusNotFound)
	adminUser, _ := h.users.GetByUsername(ctx, "admin")
	resp, raw = admin.do(http.MethodPost, "/api/namespaces/grants", map[string]any{"path": "acme", "user_id": adminUser.ID})
	wantStatus(t, "grant to admin", resp, raw, http.StatusBadRequest)
	resp, raw = admin.do(http.MethodDelete, "/api/namespaces/grants?path=acme&user_id=abc", nil)
	wantStatus(t, "revoke bad user_id", resp, raw, http.StatusBadRequest)
	resp, raw = admin.do(http.MethodGet, "/api/namespaces/grants?path=", nil)
	wantStatus(t, "list grants empty path", resp, raw, http.StatusBadRequest)

	// Grant is idempotent-safe and visible; revoke removes exactly it.
	resp, raw = admin.do(http.MethodPost, "/api/namespaces/grants", map[string]any{"path": "/acme/", "user_id": bob.ID})
	wantStatus(t, "grant (slashes trimmed)", resp, raw, http.StatusOK)
	type grants struct {
		Items []struct {
			UserID   int    `json:"user_id"`
			Username string `json:"username"`
		} `json:"items"`
	}
	_, raw = admin.do(http.MethodGet, "/api/namespaces/grants?path=acme", nil)
	if g := dataOf[grants](t, raw); len(g.Items) != 1 || g.Items[0].Username != "bob" {
		t.Fatalf("grants = %+v", g)
	}
	pats, _ := h.perms.ListForUser(ctx, bob.ID)
	if len(pats) != 1 || pats[0].RepoPattern != "acme/**" {
		t.Fatalf("stored pattern = %+v, want acme/**", pats)
	}
	resp, raw = admin.do(http.MethodDelete, fmt.Sprintf("/api/namespaces/grants?path=acme&user_id=%d", bob.ID), nil)
	wantStatus(t, "revoke", resp, raw, http.StatusOK)
	_, raw = admin.do(http.MethodGet, "/api/namespaces/grants?path=acme", nil)
	if g := dataOf[grants](t, raw); len(g.Items) != 0 {
		t.Fatalf("grants after revoke = %+v", g)
	}

	// Deleting a namespace revokes the grants that pointed at it.
	admin.do(http.MethodPost, "/api/namespaces", map[string]any{"path": "gone"})
	admin.do(http.MethodPost, "/api/namespaces/grants", map[string]any{"path": "gone", "user_id": bob.ID})
	resp, raw = admin.do(http.MethodDelete, "/api/namespaces?path=gone", nil)
	wantStatus(t, "delete ns", resp, raw, http.StatusOK)
	if pats, _ := h.perms.ListForUser(ctx, bob.ID); len(pats) != 0 {
		t.Fatalf("grant survived namespace deletion: %+v", pats)
	}
	resp, raw = admin.do(http.MethodDelete, "/api/namespaces?path=gone", nil)
	wantStatus(t, "delete missing ns", resp, raw, http.StatusNotFound)

	// A non-admin can read the tree but only sees what their patterns allow.
	admin.do(http.MethodPost, "/api/namespaces", map[string]any{"path": "visible/inner"})
	admin.do(http.MethodPost, "/api/namespaces", map[string]any{"path": "hidden/inner"})
	admin.do(http.MethodPost, "/api/namespaces/grants", map[string]any{"path": "visible", "user_id": bob.ID})
	bobS := h.session().login("bob", "bob-password-1234")
	_, raw = bobS.do(http.MethodGet, "/api/namespaces", nil)
	tree := dataOf[struct {
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
	}](t, raw)
	for _, n := range tree.Items {
		if strings.HasPrefix(n.Path, "hidden") {
			t.Fatalf("restricted user sees %q", n.Path)
		}
	}
	if len(tree.Items) == 0 {
		t.Fatal("restricted user should see the namespace they were granted")
	}
}

// --- image search ----------------------------------------------------------

func TestImageSearchAPI_Contract(t *testing.T) {
	reg := fakeRegistry(t, map[string]string{
		"git.repo":   "https://github.com/Acme/Widget-Service.git",
		"git.commit": "ABCDEF1234567890",
		"git.branch": "Release/2.0",
		"git.tag":    "v2.0.1",
		"git.hash":   "fedcba9876543210",
	})
	h := adminHarness(t, reg.URL)
	defer h.stop()
	ctx := context.Background()
	for _, tag := range []string{"v1", "v2"} {
		if err := h.index.IndexTag(ctx, "acme/app", tag); err != nil {
			t.Fatal(err)
		}
	}
	basic := func(path string, user, pass string) (*http.Response, []byte) {
		req, _ := http.NewRequest(http.MethodGet, h.baseURL+path, nil)
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp, raw
	}
	type result struct {
		Items []struct {
			Repo      string `json:"repo"`
			Tag       string `json:"tag"`
			Image     string `json:"image"`
			Digest    string `json:"digest"`
			GitRepo   string `json:"git_repo"`
			GitCommit string `json:"git_commit"`
			GitBranch string `json:"git_branch"`
			GitTag    string `json:"git_tag"`
			GitHash   string `json:"git_hash"`
			IndexedAt int64  `json:"indexed_at"`
		} `json:"items"`
		Total    int `json:"total"`
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
	}
	search := func(q string) result {
		t.Helper()
		resp, raw := basic("/api/images/search?"+q, "admin", adminPass)
		wantStatus(t, q, resp, raw, 200)
		return dataOf[result](t, raw)
	}
	bad := func(q string, want int) {
		t.Helper()
		resp, raw := basic("/api/images/search?"+q, "admin", adminPass)
		wantStatus(t, q, resp, raw, want)
	}

	// Response shape and normalisation of stored values.
	r := search("git.repo=github.com/acme/widget-service")
	if r.Total != 2 || len(r.Items) != 2 || r.Page != 0 || r.PageSize != 50 {
		t.Fatalf("shape: %+v", r)
	}
	it := r.Items[0]
	if it.Image != it.Repo+":"+it.Tag || it.Digest == "" || it.IndexedAt == 0 ||
		it.GitCommit != "abcdef1234567890" || it.GitHash != "fedcba9876543210" ||
		it.GitRepo != "https://github.com/Acme/Widget-Service.git" || it.GitBranch != "Release/2.0" || it.GitTag != "v2.0.1" {
		t.Fatalf("item fields: %+v", it)
	}

	// Exact vs fuzzy matching.
	for _, q := range []string{"git.repo=widget", "git.branch=release", "git.tag=v2.0"} {
		if got := search(q).Total; got != 0 {
			t.Fatalf("exact %s matched %d rows", q, got)
		}
		if got := search(q + "&fuzzy=true").Total; got != 2 {
			t.Fatalf("fuzzy %s matched %d rows, want 2", q, got)
		}
	}
	if got := search("git.branch=RELEASE/2.0").Total; got != 0 {
		t.Fatal("exact branch match must be case-sensitive")
	}
	if got := search("git.branch=RELEASE&fuzzy=true").Total; got != 2 {
		t.Fatal("fuzzy branch match must be case-insensitive")
	}
	if got := search("git.repo=git@github.com:ACME/Widget-Service").Total; got != 2 {
		t.Fatal("scp-style repo spelling must normalise to the same repo")
	}
	// Combined filters are AND-ed.
	if got := search("git.repo=github.com/acme/widget-service&git.branch=nope&fuzzy=true").Total; got != 0 {
		t.Fatal("filters must be AND-ed")
	}
	// Commit / hash are prefix matches regardless of case, even when fuzzy.
	if search("git.commit=ABCDEF1").Total != 2 || search("git.hash=fedcba9&fuzzy=true").Total != 2 {
		t.Fatal("commit/hash prefix lookup failed")
	}
	if search("git.commit=bcdef12").Total != 0 {
		t.Fatal("commit must match as a prefix, not a substring")
	}

	// Pagination: stable, non-overlapping, total independent of page size.
	p0 := search("git.branch=release&fuzzy=true&page_size=1&page=0")
	p1 := search("git.branch=release&fuzzy=true&page_size=1&page=1")
	p2 := search("git.branch=release&fuzzy=true&page_size=1&page=2")
	if p0.Total != 2 || p1.Total != 2 || len(p0.Items) != 1 || len(p1.Items) != 1 || len(p2.Items) != 0 {
		t.Fatalf("pages: %+v %+v %+v", p0, p1, p2)
	}
	if p0.Items[0].Image == p1.Items[0].Image {
		t.Fatal("pages overlap")
	}
	if got := search("git.branch=release&fuzzy=true&page_size=9999").PageSize; got != 200 {
		t.Fatalf("page_size must clamp to 200, got %d", got)
	}
	if got := search("git.branch=release&fuzzy=true&page_size=-3&page=-5"); got.PageSize != 50 || got.Page != 0 {
		t.Fatalf("negative paging must fall back to defaults: %+v", got)
	}

	// Validation.
	bad("", 400)
	bad("fuzzy=true", 400)
	bad("page=1", 400)
	bad("git.commit=abcdef", 400) // 6 chars < 7
	bad("git.hash=abc", 400)

	// Auth: none / wrong → 401; session cookie also accepted.
	if resp, raw := basic("/api/images/search?git.branch=x", "", ""); resp.StatusCode != 401 {
		t.Fatalf("no credentials: %d %s", resp.StatusCode, raw)
	} else if resp.Header.Get("WWW-Authenticate") == "" {
		t.Log("note: 401 carries no WWW-Authenticate challenge")
	}
	if resp, _ := basic("/api/images/search?git.branch=x", "admin", "wrong-password"); resp.StatusCode != 401 {
		t.Fatalf("wrong password: %d", resp.StatusCode)
	}
	if resp, _ := basic("/api/images/search?git.branch=x", "ghost", adminPass); resp.StatusCode != 401 {
		t.Fatalf("unknown user: %d", resp.StatusCode)
	}
	sess := h.session().login("admin", adminPass)
	resp, raw := sess.do(http.MethodGet, "/api/images/search?git.branch=release&fuzzy=true", nil)
	wantStatus(t, "session auth", resp, raw, 200)

	// Disabled users cannot search with Basic auth.
	bob, _ := h.users.Create(ctx, "bob", "bob-password-1234", "view")
	if resp, _ := basic("/api/images/search?git.branch=release&fuzzy=true", "bob", "bob-password-1234"); resp.StatusCode != 200 {
		t.Fatalf("active user: %d", resp.StatusCode)
	}
	if err := h.users.SetDisabled(ctx, bob.ID, true); err != nil {
		t.Fatal(err)
	}
	if resp, _ := basic("/api/images/search?git.branch=release&fuzzy=true", "bob", "bob-password-1234"); resp.StatusCode != 401 && resp.StatusCode != 403 {
		t.Fatalf("disabled user must be rejected, got %d", resp.StatusCode)
	}

	// Per-user visibility with pagination: a restricted user's total counts
	// only what they may see.
	carol, _ := h.users.Create(ctx, "carol", "carol-password-12", "view")
	if _, err := h.perms.GrantPatterns(ctx, carol.ID, []string{"elsewhere/**"}); err != nil {
		t.Fatal(err)
	}
	resp, raw = basic("/api/images/search?git.branch=release&fuzzy=true", "carol", "carol-password-12")
	if got := dataOf[result](t, raw); resp.StatusCode != 200 || got.Total != 0 || len(got.Items) != 0 {
		t.Fatalf("restricted user leaked results: %d %+v", resp.StatusCode, got)
	}
}

func TestOpenAPISpec_PublicAndDocumentsEndpoints(t *testing.T) {
	h := newHarness(t)
	defer h.stop()
	resp, raw := h.session().do(http.MethodGet, "/api/openapi.yaml", nil)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "yaml") {
		t.Fatalf("openapi: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	spec := string(raw)
	for _, want := range []string{"/api/images/search", "git.repo", "git.commit", "git.branch", "git.tag", "git.hash", "fuzzy", "basicAuth"} {
		if !strings.Contains(spec, want) {
			t.Errorf("openapi spec does not mention %q", want)
		}
	}
}

// --- image index rebuild -----------------------------------------------------

func TestImageIndexRebuildAPI(t *testing.T) {
	reg := fakeRegistry(t, map[string]string{"git.repo": "github.com/acme/app", "git.branch": "main"})
	h := adminHarness(t, reg.URL)
	defer h.stop()
	ctx := context.Background()
	admin := h.session().login("admin", adminPass)

	count := func() int {
		_, n, err := h.index.Search(ctx, bizFilterBranch("main"))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 0 {
		t.Fatal("index should start empty")
	}
	resp, raw := admin.do(http.MethodPost, "/api/admin/image-index/rebuild", nil)
	wantStatus(t, "rebuild", resp, raw, 200)
	if !dataOf[struct {
		Started bool `json:"started"`
	}](t, raw).Started {
		t.Fatalf("expected started=true: %s", raw)
	}
	deadline := time.Now().Add(5 * time.Second)
	for count() != 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := count(); got != 2 {
		t.Fatalf("after rebuild: %d rows, want 2 (v1+v2)", got)
	}
	// The run is audited.
	var found bool
	for end := time.Now().Add(3 * time.Second); !found && time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		_, raw = admin.do(http.MethodGet, "/api/audit?page_size=100", nil)
		found = strings.Contains(string(raw), "registry.image_index.rebuilt")
	}
	if !found {
		t.Fatal("rebuild did not write an audit entry")
	}
}

// --- backups -----------------------------------------------------------------

type backupRun struct {
	Name    string `json:"name"`
	Skipped bool   `json:"skipped"`
	Users   int    `json:"users"`
	Storage string `json:"storage"`
}
type backupList struct {
	Items []struct {
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
		Users     int    `json:"users"`
		Encrypted bool   `json:"encrypted"`
	} `json:"items"`
}
type restoreRes struct {
	UsersCreated     int      `json:"users_created"`
	UsersUpdated     int      `json:"users_updated"`
	PermissionsAdded int      `json:"permissions_added"`
	NamespacesAdded  int      `json:"namespaces_added"`
	NeedPassword     []string `json:"need_password"`
}

func TestBackupAPI_FullCycle(t *testing.T) {
	h := adminHarness(t, "http://127.0.0.1:1")
	defer h.stop()
	ctx := context.Background()
	admin := h.session().login("admin", adminPass)

	// Empty store.
	resp, raw := admin.do(http.MethodGet, "/api/admin/backups", nil)
	wantStatus(t, "list empty", resp, raw, 200)
	if l := dataOf[backupList](t, raw); l.Items == nil || len(l.Items) != 0 {
		t.Fatalf("empty list must be [], got %s", raw)
	}
	resp, raw = admin.do(http.MethodPost, "/api/admin/backups/restore", map[string]any{})
	wantStatus(t, "restore with nothing stored", resp, raw, http.StatusNotFound)

	// Build some state through the API, back it up.
	admin.do(http.MethodPost, "/api/namespaces", map[string]any{"path": "keep/me", "description": "kept"})
	resp, raw = admin.do(http.MethodPost, "/api/users", map[string]any{"username": "dana", "password": "dana-password-123", "role": "write"})
	wantStatus(t, "create user", resp, raw, 200)
	dana := dataOf[struct {
		ID int `json:"id"`
	}](t, raw)
	admin.do(http.MethodPost, "/api/namespaces/grants", map[string]any{"path": "keep", "user_id": dana.ID})

	resp, raw = admin.do(http.MethodPost, "/api/admin/backups", nil)
	wantStatus(t, "backup", resp, raw, 200)
	run := dataOf[backupRun](t, raw)
	if run.Skipped || run.Users != 2 || !strings.HasSuffix(run.Name, "_2.json") || run.Storage == "" {
		t.Fatalf("backup result: %+v", run)
	}
	_, raw = admin.do(http.MethodPost, "/api/admin/backups", nil)
	if again := dataOf[backupRun](t, raw); !again.Skipped || again.Name != "" {
		t.Fatalf("unchanged state must be skipped: %+v", again)
	}
	_, raw = admin.do(http.MethodGet, "/api/admin/backups", nil)
	list := dataOf[backupList](t, raw)
	if len(list.Items) != 1 || list.Items[0].Name != run.Name || list.Items[0].Users != 2 || list.Items[0].Encrypted {
		t.Fatalf("list: %+v", list)
	}
	if _, err := time.Parse(time.RFC3339, list.Items[0].CreatedAt); err != nil {
		t.Fatalf("created_at not RFC3339: %v", err)
	}

	// The stored object holds no secrets.
	entries, _ := os.ReadDir(h.backupDir)
	if len(entries) != 1 {
		t.Fatalf("backup dir has %d entries", len(entries))
	}
	stored, _ := os.ReadFile(filepath.Join(h.backupDir, entries[0].Name()))
	var payload struct {
		Version int `json:"version"`
		Users   []map[string]any
	}
	_ = json.Unmarshal(stored, &payload)
	if payload.Version != 1 || len(payload.Users) != 2 {
		t.Fatalf("payload: %s", stored)
	}
	for _, u := range payload.Users {
		for k := range u {
			if strings.Contains(strings.ToLower(k), "pass") || strings.Contains(strings.ToLower(k), "hash") {
				t.Fatalf("backup user carries secret field %q: %s", k, stored)
			}
		}
	}
	if strings.Contains(string(stored), "$2a$") || strings.Contains(string(stored), "dana-password-123") {
		t.Fatalf("backup leaks password material: %s", stored)
	}

	// Disaster: user, grant and namespace are gone.
	h.perms.Revoke(ctx, mustPerm(t, h, dana.ID))
	resp, raw = admin.do(http.MethodDelete, fmt.Sprintf("/api/users/%d", dana.ID), nil)
	wantStatus(t, "delete user", resp, raw, 200)
	admin.do(http.MethodDelete, "/api/namespaces?path=keep/me", nil)
	admin.do(http.MethodDelete, "/api/namespaces?path=keep", nil)

	// Bad restore targets.
	for _, name := range []string{"nope.json", "../../etc/passwd", "20990101T000000.000Z_deadbeef_1.json", "x"} {
		resp, raw = admin.do(http.MethodPost, "/api/admin/backups/restore", map[string]any{"name": name})
		wantStatus(t, "restore "+name, resp, raw, http.StatusNotFound)
	}

	resp, raw = admin.do(http.MethodPost, "/api/admin/backups/restore", map[string]any{"name": run.Name})
	wantStatus(t, "restore", resp, raw, 200)
	rr := dataOf[restoreRes](t, raw)
	if rr.UsersCreated != 1 || rr.UsersUpdated != 1 || rr.PermissionsAdded != 1 || rr.NamespacesAdded != 2 ||
		len(rr.NeedPassword) != 1 || rr.NeedPassword[0] != "dana" {
		t.Fatalf("restore result: %+v", rr)
	}
	// Restoring again is idempotent.
	_, raw = admin.do(http.MethodPost, "/api/admin/backups/restore", map[string]any{})
	if rr2 := dataOf[restoreRes](t, raw); rr2.UsersCreated != 0 || rr2.PermissionsAdded != 0 || rr2.NamespacesAdded != 0 || len(rr2.NeedPassword) != 0 {
		t.Fatalf("second restore must change nothing: %+v", rr2)
	}

	// The admin's own password was not touched; dana can't log in until reset.
	if _, err := h.users.VerifyCredentials(ctx, "admin", adminPass); err != nil {
		t.Fatalf("admin password changed by restore: %v", err)
	}
	if _, err := h.users.VerifyCredentials(ctx, "dana", "dana-password-123"); err == nil {
		t.Fatal("restored user must not accept the old password")
	}
	restored, _ := h.users.GetByUsername(ctx, "dana")
	resp, raw = admin.do(http.MethodPut, fmt.Sprintf("/api/users/%d/password", restored.ID), map[string]string{"new_password": "dana-new-password-9"})
	wantStatus(t, "reset password", resp, raw, 200)
	h.session().login("dana", "dana-new-password-9")
	_, raw = admin.do(http.MethodGet, "/api/namespaces/grants?path=keep", nil)
	if !strings.Contains(string(raw), `"dana"`) {
		t.Fatalf("grant not restored: %s", raw)
	}
}

func mustPerm(t *testing.T, h *harness, userID int) int {
	t.Helper()
	ps, err := h.perms.ListForUser(context.Background(), userID)
	if err != nil || len(ps) == 0 {
		t.Fatalf("no permission rows for user %d: %v", userID, err)
	}
	return ps[0].ID
}

func TestBackupAPI_RetentionKeepsNewest(t *testing.T) {
	h := adminHarness(t, "http://127.0.0.1:1") // harness keeps 2
	defer h.stop()
	admin := h.session().login("admin", adminPass)
	var names []string
	for i := 0; i < 4; i++ {
		u := fmt.Sprintf("user%d", i)
		resp, raw := admin.do(http.MethodPost, "/api/users", map[string]any{"username": u, "password": "password-" + u + "-xyz", "role": "view"})
		wantStatus(t, "create "+u, resp, raw, 200)
		_, raw = admin.do(http.MethodPost, "/api/admin/backups", nil)
		run := dataOf[backupRun](t, raw)
		if run.Skipped {
			t.Fatalf("backup %d skipped", i)
		}
		names = append(names, run.Name)
		time.Sleep(3 * time.Millisecond) // timestamps carry milliseconds
	}
	_, raw := admin.do(http.MethodGet, "/api/admin/backups", nil)
	list := dataOf[backupList](t, raw)
	if len(list.Items) != 2 || list.Items[0].Name != names[3] || list.Items[1].Name != names[2] {
		t.Fatalf("retention kept %+v, want newest two %v", list.Items, names[2:])
	}
}

func TestBackupAPI_EncryptedBackups(t *testing.T) {
	harnessBackupPass = "correct horse battery"
	enc := adminHarness(t, "http://127.0.0.1:1")
	harnessBackupPass = ""
	defer enc.stop()
	encAdmin := enc.session().login("admin", adminPass)
	resp, raw := encAdmin.do(http.MethodPost, "/api/admin/backups", nil)
	wantStatus(t, "encrypted backup", resp, raw, 200)
	run := dataOf[backupRun](t, raw)
	if !strings.HasSuffix(run.Name, ".json.enc") {
		t.Fatalf("encrypted backup name: %s", run.Name)
	}
	stored, _ := os.ReadFile(filepath.Join(enc.backupDir, run.Name))
	if strings.Contains(string(stored), "admin") || strings.Contains(string(stored), "version") {
		t.Fatal("encrypted backup contains plaintext")
	}
	_, raw = encAdmin.do(http.MethodGet, "/api/admin/backups", nil)
	if l := dataOf[backupList](t, raw); len(l.Items) != 1 || !l.Items[0].Encrypted {
		t.Fatalf("list must flag encryption: %+v", l)
	}

	// Same storage, no passphrase → 422; wrong passphrase → 422; right → ok.
	for _, tc := range []struct {
		pass string
		want int
	}{{"", 422}, {"wrong passphrase", 422}, {"correct horse battery", 200}} {
		harnessBackupPass = tc.pass
		other := newHarnessWithBackupDir(t, enc.backupDir)
		harnessBackupPass = ""
		_ = other.users.EnsureAdmin(context.Background(), "admin", "fresh-admin-password-1")
		s := other.session().login("admin", "fresh-admin-password-1")
		resp, raw := s.do(http.MethodPost, "/api/admin/backups/restore", map[string]any{"name": run.Name})
		wantStatus(t, fmt.Sprintf("restore with passphrase %q", tc.pass), resp, raw, tc.want)
		other.stop()
	}
}
