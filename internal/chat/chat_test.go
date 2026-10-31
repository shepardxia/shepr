package chat

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/shepardxia/shepr/internal/herdr"
)

func newRoom() *chat {
	c := newChat("w1:t1", "w1:p9")
	for i, name := range []string{"rook", "pauley"} {
		info := herdr.Agent{Name: name, PaneID: fmt.Sprintf("w1:p%d", i+2), TabID: c.tab, Status: "idle"}
		c.agents[info.PaneID] = &agent{info: info, member: true}
	}
	return c
}

func TestPrivateExchange(t *testing.T) {
	c := newRoom()
	var targets []string
	c.prompt = func(pane, _ string) error { targets = append(targets, pane); return nil }
	for _, msg := range []message{
		{author: human, text: "@rook question"},
		{pane: "w1:p2", text: "answer", audience: localAudience},
		{pane: "w1:p3", text: "room message"},
	} {
		if err := c.receive(msg); err != nil {
			t.Fatal(err)
		}
		for _, cmd := range c.dispatch() {
			c.delivered(cmd().(deliveredMsg))
		}
	}
	if !slices.Equal(targets, []string{"w1:p2", "w1:p2"}) {
		t.Fatalf("private exchange leaked: deliveries %v", targets)
	}
	if recap := c.recap(); len(recap) != 1 || recap[0].text != "room message" {
		t.Fatalf("private exchange leaked into newcomer recap: %v", recap)
	}
}

func TestDeliveryOrderAndRetry(t *testing.T) {
	for _, code := range []string{"agent_blocked", "timeout"} {
		t.Run(code, func(t *testing.T) {
			c := newRoom()
			a := c.agents["w1:p2"]
			var sent []string
			c.prompt = func(_, text string) error {
				sent = append(sent, text)
				if len(sent) == 1 {
					return &herdr.Error{Code: code}
				}
				return nil
			}
			a.pending = []string{"one"}
			first := c.dispatch()[0]
			a.pending = []string{"two"}
			if len(c.dispatch()) != 0 {
				t.Fatal("overlapping submissions")
			}
			c.delivered(first().(deliveredMsg))
			if code == "agent_blocked" && len(c.dispatch()) != 0 {
				t.Fatal("retry before roster poll")
			}
			c.syncRoster(c.members())
			for _, cmd := range c.dispatch() {
				c.delivered(cmd().(deliveredMsg))
			}
			want := "two"
			if code == "agent_blocked" {
				want = "one\n\ntwo"
			}
			if !slices.Equal(sent, []string{"one", want}) {
				t.Fatalf("sent %q", sent)
			}
		})
	}
}

func TestKickDuringDelivery(t *testing.T) {
	c := newRoom()
	c.prompt = func(_, _ string) error { return &herdr.Error{Code: "agent_blocked"} }
	a := c.agents["w1:p2"]
	a.pending = []string{"old message"}
	first := c.dispatch()[0]
	c.kick("rook")
	leave := slices.Clone(a.pending)
	if len(c.dispatch()) != 0 {
		t.Fatal("overlapping submissions after kick")
	}
	c.delivered(first().(deliveredMsg))
	if !slices.Equal(a.pending, leave) {
		t.Fatal("late refusal resurrected the old batch")
	}
}

func TestInputAndMessageLayout(t *testing.T) {
	var m tea.Model = newModel(newChat("w1:t1", "w1:p9"))
	text := "I’m adding the shared infobar contract now; I’ll expose its native view and a layout call."
	m, _ = m.Update(message{author: "wren", text: text})
	for _, width := range []int{40, 24, 60} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		c := m.(model).c
		words := strings.Fields(ansi.Strip(c.render(c.transcript[0], width-1)))
		if len(words) < 2 || strings.Join(words[2:], " ") != text {
			t.Fatalf("clipped at width %d: %v", width, words)
		}
	}
	for _, event := range []tea.Msg{
		tea.PasteMsg{Content: "one\ntwo\nthree"},
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.WindowSizeMsg{Width: 30, Height: 8},
	} {
		m, _ = m.Update(event)
		view := m.(model)
		lines := strings.Split(ansi.Strip(view.View().Content), "\n")
		if len(lines) > view.height {
			t.Fatal("input exceeds pane height")
		}
		if view.View().AltScreen {
			t.Fatal("alternate screen turns native scrolling into input keys")
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > view.width {
				t.Fatalf("line overflows: %q", line)
			}
		}
		if view.View().MouseMode != tea.MouseModeNone {
			t.Fatal("mouse capture prevents native selection")
		}
	}
}
