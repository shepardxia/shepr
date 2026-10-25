package chat

import (
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/shepardxia/shepr/internal/herdr"
)

// retryGap is how long an outbox waits after a prompt fails: herdr refuses it
// while the agent sits at an approval dialog, and reports it stalled when the
// agent never took it up.
const retryGap = 2 * time.Second

// historySize is how many messages a joining agent gets as a recap.
const historySize = 12

// delivery is text bound for the agent in pane.
type delivery struct{ pane, text string }

// receive runs on the UI loop, so the displayed messages and deliveries stay
// in the same order. Agent identity comes from its pane, not its message text.
func (c *chat) receive(msg message) *entry {
	msg.text = plainText(msg.text)
	if msg.text == "" {
		return nil
	}
	c.mu.Lock()
	if msg.pane != "" {
		a, in := c.room[msg.pane]
		if !in {
			c.mu.Unlock()
			return nil
		}
		msg.author = a.Handle()
	}
	name := c.nameStyle(msg.author, msg.pane).Render(msg.author)
	c.send(c.route(msg))
	c.mu.Unlock()
	return &entry{name + "  " + faint.Render(time.Now().Format("15:04")), c.highlight(msg.text)}
}

// route sends @messages only to their recipients. Local replies stay on
// screen; neither enters the room-wide recap. c.mu is held.
func (c *chat) route(msg message) []delivery {
	if msg.local {
		return nil
	}
	members := c.members()
	text := msg.String()
	if mentionRe.MatchString(msg.text) {
		members = mentioned(msg, members)
		text += "\n\nReply in the chat only: " + c.post + " --local \"<reply>\". Do not broadcast this exchange."
	} else {
		c.history = append(c.history, msg)
		if n := len(c.history); n > historySize {
			c.history = slices.Clone(c.history[n-historySize:])
		}
	}
	var out []delivery
	for _, a := range members {
		if a.PaneID != msg.pane {
			out = append(out, delivery{a.PaneID, text})
		}
	}
	return out
}

var mentionRe = regexp.MustCompile(`(?:^|\s)@([a-z][a-z0-9_-]{0,31})`)

// mentioned is the members msg @mentions, other than its author.
func mentioned(msg message, members []herdr.Agent) []herdr.Agent {
	names := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(msg.text, -1) {
		names[m[1]] = true
	}
	var out []herdr.Agent
	for _, a := range members {
		if names[a.Handle()] && a.PaneID != msg.pane {
			out = append(out, a)
		}
	}
	return out
}

// outbox is what waits to reach one agent. Its drain sends everything pending
// as one prompt and keeps retrying while herdr refuses it.
type outbox struct {
	pending []string
	gen     int    // counts resets, so a send in flight never trims a new batch
	stuck   string // why the last send failed, empty once one succeeds
	wake    chan struct{}
}

// send queues each delivery on its agent's outbox. c.mu is held.
func (c *chat) send(out []delivery) {
	for _, d := range out {
		c.box(d.pane).add(d.text)
	}
}

// box is pane's outbox, started on first use. c.mu is held.
func (c *chat) box(pane string) *outbox {
	b := c.boxes[pane]
	if b == nil {
		b = &outbox{wake: make(chan struct{}, 1)}
		c.boxes[pane] = b
		go c.drain(pane, b)
	}
	return b
}

func (b *outbox) add(text string) {
	b.pending = append(b.pending, text)
	b.signal()
}

// reset replaces whatever is pending with text.
func (b *outbox) reset(text string) {
	b.pending, b.gen = []string{text}, b.gen+1
	b.signal()
}

func (b *outbox) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// drain delivers pane's outbox until the agent is gone, or has left the room
// and has nothing more coming.
func (c *chat) drain(pane string, b *outbox) {
	for {
		c.mu.Lock()
		_, in := c.room[pane]
		_, live := c.agent(pane)
		if !live || (!in && len(b.pending) == 0) {
			delete(c.boxes, pane)
			c.mu.Unlock()
			return
		}
		select { // what is pending now answers every signal so far
		case <-b.wake:
		default:
		}
		batch, gen := b.pending, b.gen
		c.mu.Unlock()
		if len(batch) == 0 {
			<-b.wake
			continue
		}
		err := c.prompt(pane, strings.Join(batch, "\n\n"))
		c.mu.Lock()
		b.stuck = ""
		if err != nil {
			b.stuck = strings.TrimPrefix(herdr.Code(err), "agent_")
			if b.stuck == "" {
				b.stuck = "unreachable"
			}
		} else if b.gen == gen {
			b.pending = b.pending[len(batch):]
		}
		c.mu.Unlock()
		c.redraw()
		if err != nil {
			select {
			case <-b.wake:
			case <-time.After(retryGap):
			}
		}
	}
}

// queued is how many messages wait for pane while herdr keeps refusing them,
// and why. c.mu is held.
func (c *chat) queued(pane string) (int, string) {
	if b := c.boxes[pane]; b != nil && b.stuck != "" {
		return len(b.pending), b.stuck
	}
	return 0, ""
}
