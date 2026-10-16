package chat

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestSent(t *testing.T) {
	member := func(n string) (string, bool) { return "w1:p2", n == "dixie" }
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
		a, pane, b, ok := sent(c.in, member)
		if ok != c.ok || a != c.author || b != c.text || (ok && pane != "w1:p2") {
			t.Errorf("sent(%q) = %q, %q, %q, %v", c.in, a, pane, b, ok)
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

func TestEditKeys(t *testing.T) {
	keys := "ab\x1b[1;3Dc\x1b[1;9C"
	r := splitInput(strings.NewReader(keys), func(string) (string, bool) { return "", false }, nil)
	got, _ := io.ReadAll(r)
	if string(got) != "ab\x1bbc\x05" {
		t.Fatalf("got %q", got)
	}
}

func split(t *testing.T, parts ...string) string {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		for i, p := range parts {
			if i > 0 {
				time.Sleep(3 * burstGap)
			}
			pw.Write([]byte(p))
		}
		pw.Close()
	}()
	member := func(n string) (string, bool) { return "w1:p2", n == "dixie" }
	got, _ := io.ReadAll(splitInput(pr, member, func(string, string, string) { t.Error("taken as an agent message") }))
	return string(got)
}

func TestPaste(t *testing.T) {
	if got := split(t, "x\x1b[200~a\r\nb\rc\td\r\x1b[201~"); got != "xa⏎b⏎c    d" {
		t.Errorf("got %q", got)
	}
	if got := split(t, "\x1b[200~a\r", "b\x1b[201~"); got != "a⏎b" {
		t.Errorf("a paste split across reads: got %q", got)
	}
	if got := split(t, "\x1b[200~dixie: hi\x1b[201~"); got != "dixie: hi" {
		t.Errorf("a pasted name is still the human's text: got %q", got)
	}
}
