// Package chat is the shared chat pane for the agents in a herdr tab.
package chat

import (
	"errors"
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
type message struct {
	author, pane, text string
	local              bool // shown in the chat without prompting other agents
}

// String is the message as agents receive it.
func (m message) String() string { return m.author + ": " + m.text }

// chat is the state shared by the UI, agent posts, and roster poll. The room is the agents of the tab, less those the human removed,
// plus those the human added from other tabs; an agent is a member once
// welcomed.
type chat struct {
	tab, self string
	poke      chan struct{} // asks the roster poll to run now
	p         *tea.Program  // nil in tests
	prompt    func(pane, text string) error
	post      string // command agents use to post to this chat

	// mu is never held across a herdr call or p.Send: View takes it.
	mu      sync.Mutex
	live    []herdr.Agent          // every live agent, as of the last poll
	room    map[string]herdr.Agent // members by pane
	kicked  map[string]bool        // panes in this tab the human removed
	invited map[string]bool        // panes in other tabs the human added
	colors  map[string]int         // each member's place in palette, by pane
	boxes   map[string]*outbox
	history []message // the latest room-wide messages, for joining agents
}

func newChat(tab, self string) *chat {
	return &chat{
		tab: tab, self: self,
		poke: make(chan struct{}, 1), prompt: herdr.Prompt,
		room: map[string]herdr.Agent{}, kicked: map[string]bool{}, invited: map[string]bool{},
		colors: map[string]int{}, boxes: map[string]*outbox{},
	}
}

// Run is the chat pane for the tab in HERDR_TAB_ID.
func Run() error {
	c := newChat(os.Getenv("HERDR_TAB_ID"), os.Getenv("HERDR_PANE_ID"))
	if c.tab == "" || c.self == "" {
		return errors.New("shepr chat runs as a herdr plugin pane")
	}
	c.p = tea.NewProgram(newModel(c))
	closePosts, err := c.listenPosts()
	if err != nil {
		return err
	}
	defer closePosts()
	go c.watch()
	_, err = c.p.Run()
	return err
}

type (
	printMsg  entry    // an entry for the history
	rosterMsg struct{} // the room changed
)

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
