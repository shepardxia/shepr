// Package chat is the shared chat pane for the agents in a herdr tab.
package chat

import (
	"errors"
	"os"
	"os/user"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/shepardxia/shepr/internal/herdr"
)

var human = func() string {
	if u, err := user.Current(); err == nil {
		if f := strings.Fields(u.Name); len(f) > 0 {
			return f[0]
		}
	}
	return os.Getenv("USER")
}()

type audience uint8

const (
	roomAudience audience = iota
	directAudience
	localAudience
	noteAudience
)

// message is the source record for both display and newcomer recaps.
type message struct {
	author, pane, text string
	at                 time.Time
	audience           audience
}

func (m message) String() string { return m.author + ": " + m.text }

const (
	logSize     = 1000
	historySize = 12
)

// chat is owned entirely by the Bubble Tea loop. Commands perform I/O using
// captured arguments and return results; they never read or change this state.
type chat struct {
	tab, self  string
	agents     map[string]*agent
	colors     map[string]int
	transcript []message
	revision   uint64
	prompt     func(pane, text string) error
}

func newChat(tab, self string) *chat {
	return &chat{tab: tab, self: self, agents: map[string]*agent{}, colors: map[string]int{}, prompt: herdr.Prompt}
}

func Run() error {
	c := newChat(os.Getenv("HERDR_TAB_ID"), os.Getenv("HERDR_PANE_ID"))
	if c.tab == "" || c.self == "" {
		return errors.New("shepr chat runs as a herdr plugin pane")
	}
	p := tea.NewProgram(newModel(c))
	done := make(chan struct{})
	closePosts, err := listenPosts(p.Send, done)
	if err != nil {
		return err
	}
	defer closePosts()
	defer close(done)
	_, err = p.Run()
	return err
}

func (c *chat) record(msg message) {
	msg.at = time.Now()
	c.transcript = append(c.transcript, msg)
	if len(c.transcript) > logSize {
		c.transcript = slices.Clone(c.transcript[len(c.transcript)-logSize:])
	}
	c.revision++
}

func (c *chat) note(text string) {
	c.record(message{text: plainText(text), audience: noteAudience})
}

func (c *chat) recap() []message {
	var recent []message
	for i := len(c.transcript) - 1; i >= 0 && len(recent) < historySize; i-- {
		if msg := c.transcript[i]; msg.audience == roomAudience {
			recent = append(recent, msg)
		}
	}
	slices.Reverse(recent)
	return recent
}

func (c *chat) members() []herdr.Agent {
	var members []herdr.Agent
	for _, a := range c.agents {
		if a.member {
			members = append(members, a.info)
		}
	}
	slices.SortFunc(members, func(a, b herdr.Agent) int { return strings.Compare(a.PaneID, b.PaneID) })
	return members
}
