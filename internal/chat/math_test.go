package chat

import (
	"strings"
	"testing"
)

func TestRoomMath(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   []string
	}{
		{"Before $x^2$ after.", []string{"Before x² after."}},
		{"before\n$$\\frac{1}{2}$$\nafter", []string{"before", "─", "after"}},
		{"$$\n\\frac{1}{2}\n$$\nafter", []string{"1", "─", "2", "after"}},
		{"A `$x^2$` costs $2 or $3.", []string{"$x^2$", "$2 or $3."}},
		{"```tex\n$x^2$\n```", []string{"$x^2$"}},
		{"```math\nx^2\n```", []string{"x²"}},
	} {
		got := rendered(t, tc.source, 40)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("rendering %q: missing %q in %q", tc.source, want, got)
			}
		}
	}
}

func TestMathDelivery(t *testing.T) {
	const source = `@rook check $x^2 + \text{@pauley}$`
	c := newRoom()
	if err := c.receive(message{author: "lee", text: source}); err != nil {
		t.Fatal(err)
	}
	got := c.agents["w1:p2"].pending
	if len(got) != 1 || got[0] != "lee: "+source || len(c.agents["w1:p3"].pending) != 0 {
		t.Fatalf("wrong math delivery: %v", got)
	}
	if !strings.Contains(welcomeFor("agent", "room", nil), "Markdown and LaTeX math") {
		t.Fatal("missing room formatting hint")
	}
}
