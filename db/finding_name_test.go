package db

import (
	"strings"
	"testing"
)

func TestFindingDisplayName(t *testing.T) {
	long := strings.Repeat("a", 200)
	cases := []struct {
		name, vulnClass, summary, want string
	}{
		{"SQL injection", "SQLi", "irrelevant", "SQL injection"},
		{"  trimmed  ", "", "", "trimmed"},
		{"", "IDOR", "first line wins\nsecond line", "first line wins"},
		{"", "IDOR", "Cross-session read. Then more detail.", "Cross-session read"},
		{"", "RCE", long, strings.Repeat("a", 110) + "…"},
		{"", "auth byp / idor", "", "auth byp / idor"},
		{"", "", "", "Untitled finding"},
	}
	for _, c := range cases {
		if got := findingDisplayName(c.name, c.vulnClass, c.summary); got != c.want {
			t.Errorf("findingDisplayName(%q, %q, %.20q) = %q, want %q", c.name, c.vulnClass, c.summary, got, c.want)
		}
	}
}
