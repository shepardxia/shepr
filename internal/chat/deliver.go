package chat

import (
	"maps"
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

// held is a message that @mentions agents. It went to them first; the rest of
// the room gets it with their replies once each has replied or finished a
// turn without replying.
type held struct {
	msg     message
	first   map[string]bool   // panes msg already reached
	waiting map[string]uint64 // mentioned panes yet to answer, to their last turn when asked
	replies []*message
}

// deliver shows each queued message and routes it on.
func (c *chat) deliver() {
	for msg := range c.queue {
		c.mu.Lock()
		kicked := c.kicked[msg.pane]
		name := c.nameStyle(msg.author, msg.pane).Render(msg.author)
		c.mu.Unlock()
		if kicked {
			c.note("dropped a post from " + msg.author + ", who is not in the room")
			continue
		}
		c.print(name+"  "+faint.Render(time.Now().Format("15:04")), c.highlight(msg.text))
		if mentionRe.MatchString(msg.text) {
			// A hold notes each agent's last turn, so it needs them fresh.
			if all, err := herdr.Agents(); err == nil {
				c.mu.Lock()
				c.live = all
				c.mu.Unlock()
			}
		}
		c.act(func() ([]delivery, []string) { return c.route(msg) })
	}
}

// route is where msg goes: to the mentioned members alone, held for the rest,
// or else to every member but its author. A reply to a hold waits with it.
// c.mu is held.
func (c *chat) route(msg message) ([]delivery, []string) {
	reply := false
	for _, h := range c.holds {
		if _, ok := h.waiting[msg.pane]; ok {
			reply = true
			h.replies = append(h.replies, &msg)
			delete(h.waiting, msg.pane)
		}
	}
	out, notes := c.settle()
	if reply {
		return out, notes
	}
	h := &held{msg: msg, first: map[string]bool{}, waiting: map[string]uint64{}}
	var names []string
	for _, a := range mentioned(msg, c.members()) {
		h.first[a.PaneID] = true
		h.waiting[a.PaneID] = c.lastTurn(a.PaneID)
		names = append(names, a.Handle())
	}
	if len(h.first) == 0 || len(c.unseenBy(h)) == 0 {
		// Nobody is mentioned, or nobody is left to hold it for.
		return append(out, c.release(&held{msg: msg})...), notes
	}
	for pane := range h.first {
		out = append(out, delivery{pane, msg.String()})
	}
	c.holds = append(c.holds, h)
	return out, append(notes, "the rest wait for "+strings.Join(names, ", "))
}

// settle releases each hold no member is waiting on any more: a mentioned
// agent that left the room, or that finished a turn since it was asked
// without replying, counts as answered. c.mu is held.
func (c *chat) settle() ([]delivery, []string) {
	var notes []string
	var due, kept []*held
	for _, h := range c.holds {
		for _, pane := range slices.Sorted(maps.Keys(h.waiting)) {
			a, in := c.room[pane]
			switch {
			case !in:
				delete(h.waiting, pane)
			case c.lastTurn(pane) > h.waiting[pane] && (a.Status == "idle" || a.Status == "done"):
				delete(h.waiting, pane)
				notes = append(notes, a.Handle()+" finished without replying")
			}
		}
		if len(h.waiting) == 0 {
			due = append(due, h)
		} else {
			kept = append(kept, h)
		}
	}
	c.holds = kept
	if len(due) == 0 {
		return nil, notes
	}
	return c.release(due...), notes
}

// lastTurn is where the live agent in pane finished its latest turn. c.mu is
// held.
func (c *chat) lastTurn(pane string) uint64 {
	a, _ := c.agent(pane)
	return a.LastTurn
}

// release gives every member what it has not seen of holds and adds them to
// the history. c.mu is held.
func (c *chat) release(holds ...*held) []delivery {
	done := map[*message]bool{}
	for _, h := range holds {
		c.history = append(c.history, h.msg)
		for _, r := range h.replies {
			if !done[r] {
				done[r] = true
				c.history = append(c.history, *r)
			}
		}
	}
	if n := len(c.history); n > historySize {
		c.history = slices.Clone(c.history[n-historySize:])
	}
	return c.unseenBy(holds...)
}

// unseenBy is what each member has not seen of holds. c.mu is held.
func (c *chat) unseenBy(holds ...*held) []delivery {
	var out []delivery
	for _, a := range c.members() {
		if text := unseen(a, holds...); text != "" {
			out = append(out, delivery{a.PaneID, text})
		}
	}
	return out
}

// unseen is what agent a has not yet seen of holds: their messages, then their
// replies once each, leaving out its own and those that reached it first.
func unseen(a herdr.Agent, holds ...*held) string {
	var out []string
	for _, h := range holds {
		if h.msg.pane != a.PaneID && !h.first[a.PaneID] {
			out = append(out, h.msg.String())
		}
	}
	done := map[*message]bool{}
	for _, h := range holds {
		for _, r := range h.replies {
			if r.pane != a.PaneID && !done[r] {
				done[r] = true
				out = append(out, r.String())
			}
		}
	}
	return strings.Join(out, "\n\n")
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
