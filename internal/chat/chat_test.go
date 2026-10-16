package chat

import "testing"

func TestSent(t *testing.T) {
	member := func(n string) bool { return n == "dixie" }
	cases := []struct {
		in, author, text string
		ok               bool
	}{
		{"dixie: 4", "dixie", "4", true},
		{"dixie:4\n", "dixie", "4", true},
		{"dixie: two\nlines", "dixie", "two\nlines", true},
		{"rook: 4", "", "", false},
		{"Dixie: 4", "", "", false},
		{"dixie: ", "", "", false},
		{"d", "", "", false},
	}
	for _, c := range cases {
		a, b, ok := sent(c.in, member)
		if ok != c.ok || a != c.author || b != c.text {
			t.Errorf("sent(%q) = %q, %q, %v", c.in, a, b, ok)
		}
	}
}

func TestFreeName(t *testing.T) {
	taken := map[string]bool{"codex": true, "codex2": true}
	for kind, want := range map[string]string{"codex": "codex3", "claude": "claude", "Kimi Code": "kimi-code", "9x": "agent-9x", "": "agent"} {
		if got := freeName(kind, taken); got != want {
			t.Errorf("freeName(%q) = %q, want %q", kind, got, want)
		}
	}
}
