package server_test

import (
	"api/internal/biz"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeRegistry serves one image, acme/app:v1, whose config carries git labels.
func fakeRegistry(t *testing.T, labels map[string]string) *httptest.Server {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"config": map[string]any{"Labels": labels}})
	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json",` +
		`"config":{"mediaType":"application/vnd.docker.container.image.v1+json","size":10,"digest":"sha256:cfg"},"layers":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/_catalog":
			_, _ = w.Write([]byte(`{"repositories":["acme/app"]}`))
		case "/v2/acme/app/tags/list":
			_, _ = w.Write([]byte(`{"name":"acme/app","tags":["v1","v2"]}`))
		case "/v2/acme/app/manifests/v1", "/v2/acme/app/manifests/v2":
			w.Header().Set("Docker-Content-Digest", "sha256:aaaa")
			_, _ = w.Write([]byte(manifest))
		case "/v2/acme/app/blobs/sha256:cfg":
			_, _ = w.Write(cfg)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (h *harness) login(user, pass string) {
	h.t.Helper()
	// Prime the session cookie, as the SPA does with /auth/me on load.
	h.do(http.MethodGet, "/api/auth/me", nil)
	resp, raw := h.do(http.MethodPost, "/api/auth/login", map[string]string{"username": user, "password": pass})
	if resp.StatusCode != http.StatusOK {
		h.t.Fatalf("login %s: %d %s", user, resp.StatusCode, raw)
	}
}

func TestImageSearch_ByGitLabels(t *testing.T) {
	reg := fakeRegistry(t, map[string]string{
		"git.repo":   "git@github.com:Acme/App.git",
		"git.commit": "ABCDEF1234567890",
		"git.branch": "main",
		"git.tag":    "v1",
	})
	h := newHarnessWithUpstream(t, reg.URL)
	defer h.stop()
	ctx := context.Background()
	if err := h.users.EnsureAdmin(ctx, "admin", "a-strong-password-42"); err != nil {
		t.Fatal(err)
	}
	if err := h.index.IndexTag(ctx, "acme/app", "v1"); err != nil {
		t.Fatalf("IndexTag: %v", err)
	}

	search := func(q string, user, pass string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, h.baseURL+"/api/images/search?"+q, nil)
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		resp, err := http.DefaultClient.Do(req) // no cookie jar: Basic only
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp.StatusCode, b.String()
	}

	// Different spelling of the same repo URL, plus a commit prefix.
	code, body := search(url.Values{"git.repo": {"https://github.com/acme/app"}, "git.commit": {"abcdef1"}}.Encode(), "admin", "a-strong-password-42")
	if code != 200 || !strings.Contains(body, `"image":"acme/app:v1"`) || !strings.Contains(body, `"digest":"sha256:aaaa"`) {
		t.Fatalf("want hit, got %d %s", code, body)
	}
	if code, body = search("git.branch=dev", "admin", "a-strong-password-42"); code != 200 || !strings.Contains(body, `"total":0`) {
		t.Fatalf("want empty, got %d %s", code, body)
	}
	if code, _ = search("", "admin", "a-strong-password-42"); code != 400 {
		t.Fatalf("no filter: want 400, got %d", code)
	}
	if code, _ = search("git.commit=abc", "admin", "a-strong-password-42"); code != 400 {
		t.Fatalf("short prefix: want 400, got %d", code)
	}
	if code, _ = search("git.branch=main", "", ""); code != 401 {
		t.Fatalf("no creds: want 401, got %d", code)
	}

	// A user restricted to another namespace must not see acme/app.
	if _, err := h.users.Create(ctx, "bob", "bob-password-1234", "view"); err != nil {
		t.Fatal(err)
	}
	h.login("admin", "a-strong-password-42")
	_, raw := h.do(http.MethodGet, "/api/users", nil)
	var list struct {
		Items []struct {
			ID       int
			Username string
		}
	}
	_ = json.Unmarshal(h.decode(raw).Data, &list)
	bobID := 0
	for _, u := range list.Items {
		if u.Username == "bob" {
			bobID = u.ID
		}
	}
	if resp, raw := h.do(http.MethodPost, "/api/namespaces/grants", map[string]any{"path": "other", "user_id": bobID}); resp.StatusCode != 200 {
		t.Fatalf("grant: %d %s", resp.StatusCode, raw)
	}
	if code, body = search("git.branch=main", "bob", "bob-password-1234"); code != 200 || !strings.Contains(body, `"total":0`) {
		t.Fatalf("bob should see nothing, got %d %s", code, body)
	}
	if resp, raw := h.do(http.MethodPost, "/api/namespaces/grants", map[string]any{"path": "acme", "user_id": bobID}); resp.StatusCode != 200 {
		t.Fatalf("grant: %d %s", resp.StatusCode, raw)
	}
	if code, body = search("git.branch=main", "bob", "bob-password-1234"); code != 200 || !strings.Contains(body, `acme/app:v1`) {
		t.Fatalf("bob should see acme/app via acme/**, got %d %s", code, body)
	}
}

// The index is a cache of registry storage: wiping it and rebuilding must
// bring every git label row back.
func TestImageIndex_RebuildFromRegistry(t *testing.T) {
	reg := fakeRegistry(t, map[string]string{
		"git.repo": "https://github.com/acme/app", "git.commit": "abcdef1234567890", "git.branch": "main",
	})
	h := newHarnessWithUpstream(t, reg.URL)
	defer h.stop()
	ctx := context.Background()
	count := func() int {
		_, n, err := h.index.Search(ctx, biz.ImageSearchFilter{GitBranch: "main"})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := h.index.IndexTag(ctx, "acme/app", "v1"); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatal("want 1 indexed row")
	}
	h.index.SyncRepo(ctx, "acme/app", nil) // drops the repo's rows, like a lost database
	if count() != 0 {
		t.Fatal("rows should be gone")
	}
	repos, err := h.index.Rebuild(ctx)
	if err != nil || repos != 1 {
		t.Fatalf("Rebuild = %d, %v", repos, err)
	}
	if count() != 2 { // the registry has v1 and v2: rebuild finds both
		t.Fatalf("rebuild restored %d rows, want 2", count())
	}
}

func TestNamespaces_CreateTreeGrantDelete(t *testing.T) {
	h := newHarness(t)
	defer h.stop()
	if err := h.users.EnsureAdmin(context.Background(), "admin", "a-strong-password-42"); err != nil {
		t.Fatal(err)
	}
	h.login("admin", "a-strong-password-42")

	if resp, raw := h.do(http.MethodPost, "/api/namespaces", map[string]any{"path": "org/team", "description": "t"}); resp.StatusCode != 200 {
		t.Fatalf("create: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := h.do(http.MethodPost, "/api/namespaces", map[string]any{"path": "org/team"}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate: want 409, got %d", resp.StatusCode)
	}
	if resp, _ := h.do(http.MethodPost, "/api/namespaces", map[string]any{"path": "Bad Name"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid: want 400, got %d", resp.StatusCode)
	}

	_, raw := h.do(http.MethodGet, "/api/namespaces", nil)
	var tree struct {
		Items []struct {
			Path     string
			Explicit bool
		}
	}
	_ = json.Unmarshal(h.decode(raw).Data, &tree)
	if len(tree.Items) != 2 || tree.Items[0].Path != "org" || tree.Items[1].Path != "org/team" {
		t.Fatalf("tree = %+v (parent should be created implicitly)", tree.Items)
	}

	// Parent can't be deleted while a child exists; leaf then parent can.
	if resp, _ := h.do(http.MethodDelete, "/api/namespaces?path=org", nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete non-empty: want 409, got %d", resp.StatusCode)
	}
	for _, p := range []string{"org/team", "org"} {
		if resp, raw := h.do(http.MethodDelete, "/api/namespaces?path="+p, nil); resp.StatusCode != 200 {
			t.Fatalf("delete %s: %d %s", p, resp.StatusCode, raw)
		}
	}
}

func bizFilterBranch(b string) biz.ImageSearchFilter { return biz.ImageSearchFilter{GitBranch: b} }
