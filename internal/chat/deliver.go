package chat

import (
	"errors"
	"fmt"
	"net"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/shepardxia/shepr/internal/herdr"
)

// receive accepts a message once, before acknowledging a post or starting I/O.
func (c *chat) receive(msg message) error {
	msg.text = plainText(msg.text)
	if strings.TrimSpace(msg.text) == "" && len(msg.images) == 0 {
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
	msg.author = strings.Join(strings.Fields(plainText(msg.author)), " ")
	msg.body = parseBody(msg.text)
	targets := map[string]bool{}
	if names := msg.body.mentions(); len(names) > 0 {
		msg.audience = directAudience
		byName := map[string]string{}
		for pane, a := range c.agents {
			if a.member {
				byName[strings.ToLower(a.info.Handle())] = pane
			}
		}
		for _, name := range names {
			if strings.EqualFold(name, human) {
				continue
			}
			pane, ok := byName[strings.ToLower(name)]
			if !ok {
				return fmt.Errorf("no one in the room goes by @%s", name)
			}
			targets[pane] = true
		}
	}
	c.record(msg)
	text := msg.String()
	for pane, a := range c.agents {
		if a.member && pane != msg.pane && (msg.audience == roomAudience || targets[pane]) {
			// A directed exchange stays local until the human addresses the room.
			if msg.pane == "" || msg.audience == directAudience {
				a.localReplies = msg.audience == directAudience
			}
			a.pending = append(a.pending, text)
		}
	}
	return nil
}

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
	code := herdr.Code(msg.err)
	var network *net.OpError
	switch {
	case code == "agent_blocked", code == "agent_not_ready", errors.As(msg.err, &network) && network.Op == "dial":
		// No input was written: keep the batch until herdr is reachable and ready.
		a.pending = append(d.batch, a.pending...)
		a.waiting = strings.TrimPrefix(code, "agent_")
		if a.waiting == "" {
			a.waiting = "disconnected"
		}
	case code == "agent_not_found":
		c.note("could not send to " + a.info.Handle() + ": " + msg.err.Error())
	default:
		// A timeout or failed submission may already have written the prompt.
		c.note(fmt.Sprintf("delivery to %s could not be confirmed: %s", a.info.Handle(), msg.err))
	}
}
