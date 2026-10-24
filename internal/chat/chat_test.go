package chat

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

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

// split runs parts through splitInput, gap apart, and returns what reaches the
// input box and the posts taken out as "pane|author: text".
func split(gap time.Duration, parts ...string) (string, []string) {
	pr, pw := io.Pipe()
	go func() {
		for i, p := range parts {
			if i > 0 {
				time.Sleep(gap)
			}
			pw.Write([]byte(p))
		}
		pw.Close()
	}()
	var taken []string
	panes := map[string]string{"dixie": "w1:p2"}
	member := func(n string) string { return panes[n] }
	got, _ := io.ReadAll(splitInput(pr, member, func(author, pane, text string) { taken = append(taken, pane+"|"+author+": "+text) }))
	return string(got), taken
}

func TestSplitInput(t *testing.T) {
	apart := 3 * burstGap
	cases := []struct {
		name  string
		gap   time.Duration
		parts []string
		got   string
		taken []string
	}{
		{"typing then a post", apart, []string{"hel", "lo", "dixie: four"}, "hello", []string{"w1:p2|dixie: four"}},
		{"keystrokes right around a post", 0, []string{"h", "dixie: four", "i", "\x1b[D"}, "hi\x1b[D", []string{"w1:p2|dixie: four"}},
		{"two posts at once", 0, []string{"dixie: one", "rook: two"}, "", []string{"w1:p2|dixie: one", "|rook: two"}},
		{"a post over several reads", 0, []string{"dixie: two\nli", "nes\n"}, "", []string{"w1:p2|dixie: two\nlines"}},
		{"a name no agent holds", apart, []string{"rook: 4"}, "", []string{"|rook: 4"}},
		{"an empty post", apart, []string{"dixie:  "}, "", nil},
		{"not a name", apart, []string{"Dixie: 4", "d"}, "Dixie: 4d", nil},
		{"a paste starting with a name", apart, []string{"\x1b[200~dixie: pasted\x1b[201~"}, "\x1b[200~dixie: pasted\x1b[201~", nil},
		{"a paste over several reads", apart, []string{"\x1b[200~a\r", "dixie: b\x1b[201~"}, "\x1b[200~a\rdixie: b\x1b[201~", nil},
	}
	for _, c := range cases {
		got, taken := split(c.gap, c.parts...)
		if got != c.got || strings.Join(taken, ",") != strings.Join(c.taken, ",") {
			t.Errorf("%s: got %q, taken %q", c.name, got, taken)
		}
	}
}

func TestUnseen(t *testing.T) {
	rook, pauley := herdr.Agent{PaneID: "w1:p2"}, herdr.Agent{PaneID: "w1:p3"}
	reply := &message{"rook", "w1:p2", "because"}
	why := &held{msg: message{"Shepard", "", "@rook why?"}, first: map[string]bool{"w1:p2": true}, replies: []*message{reply}}
	if got := unseen(rook, why); got != "" {
		t.Errorf("rook saw both already, got %q", got)
	}
	if got := unseen(pauley, why); got != "Shepard: @rook why?\n\nrook: because" {
		t.Errorf("pauley gets the question and the reply, got %q", got)
	}
	how := &held{msg: message{"Shepard", "", "@rook how?"}, first: map[string]bool{"w1:p2": true}, replies: []*message{reply}}
	if got := unseen(pauley, why, how); got != "Shepard: @rook why?\n\nShepard: @rook how?\n\nrook: because" {
		t.Errorf("one reply to two questions comes once, got %q", got)
	}
}

func TestMentioned(t *testing.T) {
	members := []herdr.Agent{{Name: "rook", PaneID: "w1:p2"}, {Name: "pauley", PaneID: "w1:p3"}}
	got := mentioned(message{"Shepard", "", "@rook and mail me at a@pauley.dev"}, members)
	if len(got) != 1 || got[0].Name != "rook" {
		t.Errorf("only the @ at a word start counts, got %+v", got)
	}
	if got := mentioned(message{"rook", "w1:p2", "@rook talking to myself"}, members); len(got) != 0 {
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
	ask := message{human, "", "@rook why?"}
	steps := []struct {
		msg  message
		want string
	}{
		{message{human, "", "hi"}, "w1:p2<" + human + ": hi | w1:p3<" + human + ": hi"},
		{message{"rook", "w1:p2", "hey"}, "w1:p3<rook: hey"},
		{ask, "w1:p2<" + ask.String()},
		{message{"pauley", "w1:p3", "meanwhile"}, "w1:p2<pauley: meanwhile"},
		{message{"rook", "w1:p2", "because"}, "w1:p3<" + ask.String() + "\n\nrook: because"},
	}
	for i, s := range steps {
		out, _ := c.route(s.msg)
		if got := show(out); got != s.want {
			t.Errorf("step %d: got %q, want %q", i, got, s.want)
		}
	}
	if len(c.holds) != 0 || len(c.history) != 5 {
		t.Errorf("the hold should be released into the history, holds %d, history %d", len(c.holds), len(c.history))
	}
}

func TestHoldSettles(t *testing.T) {
	c := newRoom()
	ask := message{human, "", "@rook why?"}
	c.route(ask)
	if out, _ := c.settle(); len(out) != 0 {
		t.Fatalf("rook has not finished a turn, got %q", show(out))
	}
	c.live[0].LastTurn, c.live[0].Status = 1, "done"
	c.room["w1:p2"] = c.live[0]
	out, notes := c.settle()
	if show(out) != "w1:p3<"+ask.String() || len(notes) != 1 {
		t.Errorf("a finished turn without a reply releases the hold, got %q %q", show(out), notes)
	}
	c.route(ask)
	delete(c.room, "w1:p2")
	if out, _ := c.settle(); show(out) != "w1:p3<"+ask.String() {
		t.Errorf("a mentioned agent leaving releases the hold, got %q", show(out))
	}
	if out, _ := c.route(message{"pauley", "w1:p3", "@rook?"}); len(out) != 0 || len(c.holds) != 0 {
		t.Errorf("a mention of no member goes to every member but its author, got %q", show(out))
	}
	if out, _ := c.route(message{human, "", "@pauley alone"}); show(out) != "w1:p3<"+human+": @pauley alone" || len(c.holds) != 0 {
		t.Errorf("with no one else to hold for, nothing is held, got %q", show(out))
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
	joining, _, notes := c.update(all)
	var got []string
	for _, a := range joining {
		got = append(got, a.PaneID)
	}
	if strings.Join(got, " ") != "w1:p5 w2:p1" {
		t.Errorf("joining %v: the ready agents of the tab not removed, and those added", got)
	}
	if strings.Join(notes, "|") != faint.Render("  rook is now dixie")+"|"+faint.Render("  pauley left") && strings.Join(notes, "|") != faint.Render("  pauley left")+"|"+faint.Render("  rook is now dixie") {
		t.Errorf("notes %q", notes)
	}
	if c.room["w1:p2"].Name != "dixie" || c.names["dixie"] != "w1:p2" {
		t.Errorf("the rename should reach the room and the names, got %+v", c.room)
	}
}

func TestKick(t *testing.T) {
	c := newRoom()
	got := make(chan string, 2)
	c.prompt = func(pane, text string) error {
		got <- pane + "<" + text
		return nil
	}
	c.route(message{human, "", "@rook why?"})
	if note := c.kick("rook"); note != "  rook left the room" {
		t.Errorf("note %q", note)
	}
	sent := []string{<-got, <-got}
	sort.Strings(sent)
	if want := []string{"w1:p2<" + fmt.Sprintf(leaveText, human, c.self), "w1:p3<" + human + ": @rook why?"}; sent[0] != want[0] || sent[1] != want[1] {
		t.Errorf("rook should be told it left, and pauley get the released question, got %q", sent)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, in := c.room["w1:p2"]; in || !c.kicked["w1:p2"] || len(c.holds) != 0 {
		t.Errorf("rook should be out, kicked, and the hold released: %+v", c.holds)
	}
	if joining, _, _ := c.update(c.live); len(joining) != 0 {
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
	if got := welcomeFor("rook", "w1:p9", nil); strings.Contains(got, "so far") {
		t.Errorf("no recap without history")
	}
	got := welcomeFor("rook", "w1:p9", []message{{"pauley", "w1:p3", "hi"}})
	if !strings.HasSuffix(got, "latest last:\n\npauley: hi") {
		t.Errorf("recap missing: %q", got[len(got)-60:])
	}
}
