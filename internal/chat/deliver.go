package chat

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/shepardxia/shepr/internal/herdr"
)

// receive accepts a message once, before acknowledging a post or starting I/O.
func (c *chat) receive(msg message) error {
	msg.text = plainText(msg.text)
	if msg.text == "" {
		return errors.New("message is empty")
	}
	if msg.pane != "" {
		a := c.agents[msg.pane]
		if a == nil || !a.member {
			return errors.New("your agent is not in this room")
		}
		if msg.author == "" {
			msg.author = a.info.Handle()
		}
		if a.localReplies {
			msg.audience = localAudience
		}
	}
	names := map[string]bool{}
	if msg.audience != localAudience {
		for _, match := range mentionRe.FindAllStringSubmatch(msg.text, -1) {
			msg.audience = directAudience
			names[match[1]] = true
		}
	}
	c.record(msg)
	text := msg.String()
	for pane, a := range c.agents {
		if a.member && pane != msg.pane && (msg.audience == roomAudience || names[a.info.Handle()]) {
			// A directed exchange stays local until the human addresses the room.
			if msg.pane == "" || msg.audience == directAudience {
				a.localReplies = msg.audience == directAudience
			}
			a.pending = append(a.pending, text)
		}
	}
	return nil
}

var mentionRe = regexp.MustCompile(`(?:^|\s)@([a-z][a-z0-9_-]{0,31})`)

// attempt identifies the one batch in flight for a pane. Resetting membership
// discards its result but still waits for it before sending the next batch.
type attempt struct {
	pane    string
	batch   []string
	discard bool
}

type deliveredMsg struct {
	attempt *attempt
	err     error
}

func (a *agent) replacePending(text string) {
	a.pending, a.waiting, a.localReplies = nil, "", false
	if text != "" {
		a.pending = []string{text}
	}
	if a.sending != nil {
		a.sending.discard = true
	}
}

// dispatch starts at most one submission per agent. A refused batch waits for
// the next roster poll, not for another timer or an incoming chat message.
func (c *chat) dispatch() []tea.Cmd {
	var cmds []tea.Cmd
	for pane, a := range c.agents {
		if a.sending != nil || a.waiting != "" || len(a.pending) == 0 {
			continue
		}
		if a.info.Status == "blocked" {
			a.waiting = "blocked"
			continue
		}
		d := &attempt{pane: pane, batch: a.pending}
		a.sending, a.pending = d, nil
		text, prompt := strings.Join(d.batch, "\n\n"), c.prompt
		cmds = append(cmds, func() tea.Msg {
			return deliveredMsg{d, prompt(pane, text)}
		})
	}
	return cmds
}

func (c *chat) delivered(msg deliveredMsg) {
	d := msg.attempt
	a := c.agents[d.pane]
	if a == nil || a.sending != d {
		return // the agent disappeared while this request was in flight
	}
	a.sending = nil
	if d.discard || msg.err == nil {
		return
	}
	switch herdr.Code(msg.err) {
	case "agent_blocked", "agent_not_ready":
		// Herdr rejects these before writing any input to the agent.
		a.pending = append(d.batch, a.pending...)
		a.waiting = strings.TrimPrefix(herdr.Code(msg.err), "agent_")
	case "agent_not_found":
		c.note("could not send to " + a.info.Handle() + ": " + msg.err.Error())
	default:
		// A timeout or failed submission may already have written the prompt.
		c.note(fmt.Sprintf("delivery to %s could not be confirmed: %s", a.info.Handle(), msg.err))
	}
}
