// Package chat is the chat pane: it shows every message, takes the human's
// from an input box and the agents' from herdr pane send-text, and delivers
// each to every other agent in the room.
package chat

import (
	"errors"
	"io"
	"os"
	"os/user"
	"slices"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/shepardxia/shepr/internal/herdr"
)

// human is the name the person at the input box posts under: the first name
// on the account, else the login.
var human = func() string {
	if u, err := user.Current(); err == nil {
		if f := strings.Fields(u.Name); len(f) > 0 {
			return f[0]
		}
	}
	return os.Getenv("USER")
}()

// message is one chat message. pane is its author's pane, empty for the human.
type message struct{ author, pane, text string }

// String is the message as agents receive it.
func (m message) String() string { return m.author + ": " + m.text }

// chat is the state the input box, the delivery loop and the roster poll
// share. The room is the agents of the tab, less those the human removed,
// plus those the human added from other tabs; an agent is a member once
// welcomed.
type chat struct {
	tab, self string
	queue     chan message  // shown and routed one at a time, in order
	poke      chan struct{} // asks the roster poll to run now
	p         *tea.Program  // nil in tests
	prompt    func(pane, text string) error

	// mu is never held across a herdr call or p.Send: View takes it.
	mu      sync.Mutex
	live    []herdr.Agent          // every live agent, as of the last poll
	room    map[string]herdr.Agent // members by pane
	kicked  map[string]bool        // panes in this tab the human removed
	invited map[string]bool        // panes in other tabs the human added
	names   map[string]string      // every name a member has held, to its pane
	colors  map[string]int         // each member's place in palette, by pane
	boxes   map[string]*outbox
	holds   []*held
	history []message // the latest messages every member got
}

func newChat(tab, self string) *chat {
	return &chat{
		tab: tab, self: self,
		queue: make(chan message, 64), poke: make(chan struct{}, 1), prompt: herdr.Prompt,
		room: map[string]herdr.Agent{}, kicked: map[string]bool{}, invited: map[string]bool{},
		names: map[string]string{}, colors: map[string]int{}, boxes: map[string]*outbox{},
	}
}

// Run is the chat pane for the tab in HERDR_TAB_ID.
func Run() error {
	c := newChat(os.Getenv("HERDR_TAB_ID"), os.Getenv("HERDR_PANE_ID"))
	if c.tab == "" || c.self == "" {
		return errors.New("shepr chat runs as a herdr plugin pane")
	}
	in := terminal{splitInput(os.Stdin, c.member, func(author, pane, text string) {
		c.queue <- message{author, pane, text}
	}), os.Stdin}
	c.p = tea.NewProgram(newModel(c), tea.WithInput(in))
	go c.deliver()
	go c.watch()
	_, err := c.p.Run()
	return err
}

// terminal is the pane's terminal as the program sees it: reads come through
// splitInput, while Fd stays the terminal's, so the program sets raw mode and
// renders for a raw terminal. It has no Name method, which keeps the program
// reading through Read rather than polling the descriptor.
type terminal struct {
	io.Reader
	tty *os.File
}

func (t terminal) Write(b []byte) (int, error) { return t.tty.Write(b) }
func (t terminal) Close() error                { return nil }
func (t terminal) Fd() uintptr                 { return t.tty.Fd() }

type (
	printMsg  entry    // an entry for the history
	rosterMsg struct{} // the room changed
)

// print adds a message under head to the history. It blocks until the program
// takes it, so it never runs under mu; nor does note.
func (c *chat) print(head, text string) {
	if c.p != nil {
		c.p.Send(printMsg{head, text})
	}
}

// note adds notes on the room to the history.
func (c *chat) note(notes ...string) {
	for _, n := range notes {
		if c.p != nil {
			c.p.Send(printMsg{text: n})
		}
	}
}

func (c *chat) redraw() {
	if c.p != nil {
		c.p.Send(rosterMsg{})
	}
}

// pokeWatch runs the roster poll now rather than at its next tick.
func (c *chat) pokeWatch() {
	select {
	case c.poke <- struct{}{}:
	default:
	}
}

// act runs f under mu, queues the deliveries it returns, then prints its
// notes and redraws.
func (c *chat) act(f func() ([]delivery, []string)) {
	c.mu.Lock()
	out, notes := f()
	c.send(out)
	c.mu.Unlock()
	c.note(notes...)
	c.redraw()
}

// agent is the live agent in pane. c.mu is held.
func (c *chat) agent(pane string) (herdr.Agent, bool) {
	for _, a := range c.live {
		if a.PaneID == pane {
			return a, true
		}
	}
	return herdr.Agent{}, false
}

// members is the room in pane order. c.mu is held.
func (c *chat) members() []herdr.Agent {
	ms := make([]herdr.Agent, 0, len(c.room))
	for _, a := range c.room {
		ms = append(ms, a)
	}
	slices.SortFunc(ms, func(a, b herdr.Agent) int { return strings.Compare(a.PaneID, b.PaneID) })
	return ms
}

// roster is the room in pane order.
func (c *chat) roster() []herdr.Agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.members()
}

// member resolves name to the pane of the live agent that goes by it, asking
// herdr afresh on a miss so a post sent right after a rename is not delivered
// back to its author, and else to the member that last held it. A name no
// agent holds or a member held resolves to no pane.
func (c *chat) member(name string) string {
	find := func(as []herdr.Agent) string {
		for _, a := range as {
			if a.Handle() == name {
				return a.PaneID
			}
		}
		return ""
	}
	if pane := find(c.roster()); pane != "" {
		return pane
	}
	if all, err := herdr.Agents(); err == nil {
		if pane := find(all); pane != "" {
			return pane
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.names[name]
}
