package biz

import "testing"

func TestNormalizeGitRepo(t *testing.T) {
	cases := map[string]string{
		"https://github.com/Org/App.git":      "github.com/org/app",
		"git@github.com:org/app.git":          "github.com/org/app",
		"github.com/org/app":                  "github.com/org/app",
		"ssh://git@gitlab.local:2222/a/b.git": "gitlab.local:2222/a/b",
		"https://user:tok@host.io/a/b/":       "host.io/a/b",
		"  ":                                  "",
	}
	for in, want := range cases {
		if got := NormalizeGitRepo(in); got != want {
			t.Errorf("NormalizeGitRepo(%q)=%q want %q", in, got, want)
		}
	}
}
