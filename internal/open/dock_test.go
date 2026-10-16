package open

import (
	"os"
	"testing"
)

// tree builds a layout from a preorder spec: "d" or "r" opens a down or right
// split, "p" takes the next pane.
func tree(spec string, panes ...string) *node {
	i, j := 0, 0
	var build func() *node
	build = func() *node {
		c := spec[i]
		i++
		if c == 'p' {
			j++
			return &node{Type: "pane", PaneID: panes[j-1]}
		}
		dir := map[byte]string{'d': "down", 'r': "right"}[c]
		return &node{Type: "split", Direction: dir, Ratio: 0.5, First: build(), Second: build()}
	}
	return build()
}

func TestRebuildOrder(t *testing.T) {
	n := tree("rdppdprpp", "a", "b", "c", "d", "e")
	var got []string
	err := rebuild(n, func(p, target, direction string, _ float64) error {
		got = append(got, p+" "+direction+" of "+target)
		return nil
	})
	want := []string{"c right of a", "b down of a", "d down of c", "e right of d"}
	if err != nil || len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestWithout(t *testing.T) {
	n := tree("rdppp", "a", "chat", "c")
	rest := n.without("chat")
	if rest.Direction != "right" || rest.First.PaneID != "a" || rest.Second.PaneID != "c" {
		t.Fatalf("collapse failed: %+v", rest)
	}
	if n.without("zzz") == nil || len(n.without("zzz").panes()) != 3 {
		t.Fatal("an absent pane should leave every pane in place")
	}
}

// TestDockLive rearranges a real herdr tab, so it runs only when
// SHEPR_LIVE_TAB and SHEPR_LIVE_CHAT name a tab and the chat pane in it.
func TestDockLive(t *testing.T) {
	tab, chat := os.Getenv("SHEPR_LIVE_TAB"), os.Getenv("SHEPR_LIVE_CHAT")
	if tab == "" || chat == "" {
		t.Skip("set SHEPR_LIVE_TAB and SHEPR_LIVE_CHAT")
	}
	if err := dock(os.Getenv("HERDR_WORKSPACE_ID"), tab, chat); err != nil {
		t.Fatal(err)
	}
}
