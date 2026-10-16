package chat

import (
	"io"
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

// split runs parts through splitInput, pausing between them, and returns what
// reaches the input box and the agent messages taken out.
func split(parts ...string) (string, []string) {
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
	var taken []string
	member := func(n string) (string, bool) { return "w1:p2", n == "dixie" }
	got, _ := io.ReadAll(splitInput(pr, member, func(author, _, text string) { taken = append(taken, author+": "+text) }))
	return string(got), taken
}

func TestSplitInput(t *testing.T) {
	if got, taken := split("hel", "lo", "dixie: four"); got != "hello" || len(taken) != 1 || taken[0] != "dixie: four" {
		t.Errorf("got %q, taken %q", got, taken)
	}
	paste := "\x1b[200~dixie: pasted\x1b[201~"
	if got, taken := split(paste); got != paste || len(taken) != 0 {
		t.Errorf("a paste starting with a name is the human's: got %q, taken %q", got, taken)
	}
	if got, taken := split("\x1b[200~a\r", "dixie: b\x1b[201~"); got != "\x1b[200~a\rdixie: b\x1b[201~" || len(taken) != 0 {
		t.Errorf("a paste split across reads stays whole: got %q, taken %q", got, taken)
	}
}
