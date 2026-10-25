package chat

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shepardxia/shepr/internal/herdr"
)

func TestFreeName(t *testing.T) {
	taken := map[string]bool{"codex": true, "codex2": true}
	for kind, want := range map[string]string{"codex": "codex3", "claude": "claude", "Kimi Code": "kimi-code", "9x": "agent-9x", "": "agent"} {
		if got := freeName(kind, taken); got != want {
			t.Errorf("freeName(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestMentioned(t *testing.T) {
	members := []herdr.Agent{{Name: "rook", PaneID: "w1:p2"}, {Name: "pauley", PaneID: "w1:p3"}}
	got := mentioned(message{author: "Shepard", text: "@rook and mail me at a@pauley.dev"}, members)
	if len(got) != 1 || got[0].Name != "rook" {
		t.Errorf("only the @ at a word start counts, got %+v", got)
	}
	if got := mentioned(message{author: "rook", pane: "w1:p2", text: "@rook talking to myself"}, members); len(got) != 0 {
		t.Errorf("an author does not mention itself, got %+v", got)
	}
}

// newRoom is a chat whose room holds rook (w1:p2) and pauley (w1:p3).
func newRoom() *chat {
	c := newChat("w1:t1", "w1:p9")
	for _, a := range []herdr.Agent{{Name: "rook", PaneID: "w1:p2", TabID: "w1:t1", Status: "idle"}, {Name: "pauley", PaneID: "w1:p3", TabID: "w1:t1", Status: "idle"}} {
		c.room[a.PaneID] = a
	}
	c.live = c.members()
	return c
}

// show lists deliveries as "pane<text", sorted.
func show(out []delivery) string {
	var s []string
	for _, d := range out {
		s = append(s, d.pane+"<"+d.text)
	}
	sort.Strings(s)
	return strings.Join(s, " | ")
}

func TestRoute(t *testing.T) {
	c := newRoom()
	c.post = "shepr post room.sock"
	ask := message{author: human, text: "@rook why?"}
	steps := []struct {
		msg  message
		want string
	}{
		{message{author: human, text: "hi"}, "w1:p2<" + human + ": hi | w1:p3<" + human + ": hi"},
		{message{author: "rook", pane: "w1:p2", text: "hey"}, "w1:p3<rook: hey"},
		{ask, "w1:p2<" + ask.String() + "\n\nReply in the chat only: shepr post room.sock --local \"<reply>\". Do not broadcast this exchange."},
		{message{author: "rook", pane: "w1:p2", text: "because", local: true}, ""},
		{message{author: human, text: "@nobody typo"}, ""},
		{message{author: "pauley", pane: "w1:p3", text: "meanwhile"}, "w1:p2<pauley: meanwhile"},
	}
	for i, s := range steps {
		if got := show(c.route(s.msg)); got != s.want {
			t.Errorf("step %d: got %q, want %q", i, got, s.want)
		}
	}
	if len(c.history) != 3 {
		t.Errorf("only room-wide messages belong in the recap, got %d", len(c.history))
	}
}

func TestUpdate(t *testing.T) {
	c := newRoom()
	c.kicked["w1:p4"] = true
	c.invited["w2:p1"] = true
	all := []herdr.Agent{
		{Name: "dixie", PaneID: "w1:p2", TabID: "w1:t1", Status: "idle"}, // rook renamed
		{Name: "kestrel", PaneID: "w1:p4", TabID: "w1:t1", Status: "idle"},
		{PaneID: "w1:p5", TabID: "w1:t1", Status: "working"},
		{PaneID: "w1:p6", TabID: "w1:t1", Status: "blocked"},
		{Name: "vesper", PaneID: "w2:p1", TabID: "w2:t1", Status: "idle"},
		{Name: "oriole", PaneID: "w2:p2", TabID: "w2:t1", Status: "idle"},
	}
	joining, notes := c.update(all)
	var got []string
	for _, a := range joining {
		got = append(got, a.PaneID)
	}
	if strings.Join(got, " ") != "w1:p5 w2:p1" {
		t.Errorf("joining %v: the ready agents of the tab not removed, and those added", got)
	}
	if strings.Join(notes, "|") != "rook is now dixie|pauley left" {
		t.Errorf("notes %q", notes)
	}
	if c.room["w1:p2"].Name != "dixie" {
		t.Errorf("the rename should reach the room, got %+v", c.room)
	}
}

func TestKick(t *testing.T) {
	c := newRoom()
	got := make(chan string, 2)
	c.prompt = func(pane, text string) error {
		got <- pane + "<" + text
		return nil
	}
	if note := c.kick("rook"); note != "rook left the room" {
		t.Errorf("note %q", note)
	}
	if sent := <-got; sent != "w1:p2<"+fmt.Sprintf(leaveText, human, c.self) {
		t.Errorf("rook should be told it left, got %q", sent)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, in := c.room["w1:p2"]; in || !c.kicked["w1:p2"] {
		t.Error("rook should be out of the room and kicked")
	}
	if joining, _ := c.update(c.live); len(joining) != 0 {
		t.Errorf("a kicked agent does not rejoin, got %+v", joining)
	}
}

func TestOutboxRetries(t *testing.T) {
	c := newRoom()
	tries := make(chan string)
	fail := true
	c.prompt = func(_, text string) error {
		tries <- text
		if fail {
			fail = false
			return &herdr.Error{Code: "agent_blocked"}
		}
		return nil
	}
	c.mu.Lock()
	c.box("w1:p2").add("one")
	c.mu.Unlock()
	if got := <-tries; got != "one" {
		t.Fatalf("first try %q", got)
	}
	time.Sleep(10 * time.Millisecond)
	c.mu.Lock()
	if n, why := c.queued("w1:p2"); n != 1 || why != "blocked" {
		t.Errorf("a refused send stays queued, got %d %q", n, why)
	}
	c.box("w1:p2").add("two")
	c.mu.Unlock()
	if got := <-tries; got != "one\n\ntwo" {
		t.Errorf("the retry sends everything pending as one prompt, got %q", got)
	}
	time.Sleep(10 * time.Millisecond)
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, _ := c.queued("w1:p2"); n != 0 || len(c.boxes["w1:p2"].pending) != 0 {
		t.Errorf("a delivered batch leaves the outbox")
	}
}

func TestWelcomeRecap(t *testing.T) {
	if got := welcomeFor("rook", "w1:p9", "shepr post room.sock", nil); strings.Contains(got, "missed") {
		t.Errorf("no recap without history")
	}
	got := welcomeFor("rook", "w1:p9", "shepr post room.sock", []message{{author: "pauley", pane: "w1:p3", text: "hi"}})
	if !strings.HasSuffix(got, "latest last:\n\npauley: hi") {
		t.Errorf("recap missing: %q", got[len(got)-60:])
	}
}

func TestGiveColor(t *testing.T) {
	c := newChat("w1:t1", "w1:p9")
	join := func(pane string) int {
		c.room[pane] = herdr.Agent{PaneID: pane}
		c.giveColor(pane)
		return c.colors[pane]
	}
	seen := map[int]bool{}
	for i := range palette {
		seen[join(fmt.Sprintf("w1:p%d", i))] = true
	}
	if len(seen) != len(palette) {
		t.Errorf("members up to the palette's size each get their own color, got %v", c.colors)
	}
	sixth := join("w1:pX")
	delete(c.room, "w1:p0")
	delete(c.room, "w1:pX")
	if again := join("w1:pX"); again != sixth {
		t.Errorf("a member rejoining keeps a color no one else holds, got %d after %d", again, sixth)
	}
	delete(c.room, "w1:p1")
	c.colors["w1:pY"] = c.colors["w1:p2"]
	if got := join("w1:pY"); got == c.colors["w1:p2"] {
		t.Errorf("a member whose old color is taken gets another, got %d", got)
	}
}

func TestHistoryRewraps(t *testing.T) {
	text := "I’m adding the shared infobar contract now; I’ll expose its native view and a layout call."
	words := map[string]bool{}
	for _, w := range strings.Fields(text) {
		words[w] = true
	}
	var m tea.Model = newModel(newChat("w1:t1", "w1:p9"))
	m, _ = m.Update(printMsg{"wren", text})
	for _, width := range []int{40, 24, 60} {
		rendered := ansi.Strip((entry{"wren", text}).render(width - 1))
		if got := strings.Join(strings.Fields(rendered), " "); got != "wren "+text {
			t.Errorf("width %d lost text while wrapping: %q", width, got)
		}
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		h := m.(model).history
		lines := strings.Split(ansi.Strip(h.View()), "\n")
		if !strings.Contains(lines[len(lines)-1], "call.") {
			t.Errorf("width %d: the history should end at its latest line, got %q", width, lines)
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > width {
				t.Errorf("width %d: line %q overflows", width, l)
			}
			for _, w := range strings.Fields(l) {
				if !words[w] && w != "wren" {
					t.Errorf("width %d: %q is a broken word", width, w)
				}
			}
		}
	}
}

func TestHistoryFollows(t *testing.T) {
	var m tea.Model = newModel(newChat("w1:t1", "w1:p9"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 12})
	for i := range 20 {
		m, _ = m.Update(printMsg{"wren", fmt.Sprintf("line %d of a chat long enough to scroll", i)})
	}
	last := func() string {
		lines := strings.Split(ansi.Strip(m.(model).history.View()), "\n")
		return lines[len(lines)-1]
	}
	steps := []struct {
		name string
		msg  tea.Msg
	}{
		{"a shorter pane", tea.WindowSizeMsg{Width: 60, Height: 8}},
		{"a narrower pane", tea.WindowSizeMsg{Width: 30, Height: 8}},
		{"a taller input box", tea.PasteMsg{Content: "one\ntwo\nthree"}},
		{"clearing multiline input", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}},
		{"pasting again", tea.PasteMsg{Content: "one\ntwo\nthree"}},
		{"submitting multiline input", tea.KeyPressMsg{Code: tea.KeyEnter}},
		{"recalling multiline input", tea.KeyPressMsg{Code: tea.KeyUp}},
		{"restoring the draft", tea.KeyPressMsg{Code: tea.KeyDown}},
	}
	for _, s := range steps {
		m, _ = m.Update(s.msg)
		view := m.(model)
		if !view.history.AtBottom() {
			t.Errorf("after %s the history should follow the latest message, got %q", s.name, last())
		}
		lines := strings.Split(ansi.Strip(view.View().Content), "\n")
		if len(lines) != view.height {
			t.Errorf("after %s frame has %d rows for height %d", s.name, len(lines), view.height)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > view.width {
				t.Errorf("after %s frame overflows: %q", s.name, line)
			}
		}
	}
}
