package db

import "testing"

func TestTaskDisplayName(t *testing.T) {
	cases := []struct {
		name        string
		given       string
		description string
		want        string
	}{
		{"explicit name wins", "rce hunt", "Deploy a pivot proxy", "rce hunt"},
		{"derived from description", "", "Deploy pivot proxy to internal NSI API via Liferay BeanShell foothold", "Deploy pivot proxy to internal NSI API via Liferay BeanShell foothold"},
		{"first sentence only", "", "Perform a full-depth test. Use all techniques.", "Perform a full-depth test"},
		{"first line only", "", "Find new RCE/shell access on plus.pl\nSecondary objective follows", "Find new RCE/shell access on plus.pl"},
		{"long description truncated", "", string(make([]byte, 0)) + longRunes(150), string(longRunes(110)) + "…"},
		{"blank everything", "", "   ", "Untitled task"},
	}
	for _, c := range cases {
		if got := taskDisplayName(c.given, c.description); got != c.want {
			t.Errorf("%s: taskDisplayName(%q, %q) = %q, want %q", c.name, c.given, c.description, got, c.want)
		}
	}
}

func longRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
