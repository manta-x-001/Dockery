package scope

import "testing"

func TestMatchPattern_Recursive(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"org/**", "org/app", true},
		{"org/**", "org/team/app", true},
		{"org/**", "org", false},
		{"org/**", "organic/app", false},
		{"org/team/**", "org/app", false},
	}
	for _, c := range cases {
		if got := MatchPattern(c.pattern, c.name); got != c.want {
			t.Errorf("MatchPattern(%q,%q)=%v want %v", c.pattern, c.name, got, c.want)
		}
	}
}
