package chat

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/shepardxia/shepr/internal/herdr"
)

const pollGap = 2 * time.Second

type membership uint8

const (
	automatic membership = iota
	included
	excluded
)

// agent holds the last roster snapshot and this room's decisions for one pane.
// It disappears when the pane's agent does; outstanding results then go stale.
type agent struct {
	info         herdr.Agent
	membership   membership
	member       bool
	pending      []string
	sending      *attempt
	waiting      string
	localReplies bool
}

type pollMsg struct{}
type rosterMsg struct {
	agents []herdr.Agent
	err    error
}

func pollRoster() tea.Msg {
	agents, err := herdr.Agents()
	return rosterMsg{agents, err}
}

func (c *chat) wanted(a *agent) bool {
	return a.membership != excluded && (a.info.TabID == c.tab || a.membership == included)
}

// syncRoster reconciles membership and releases refused deliveries for retry.
func (c *chat) syncRoster(all []herdr.Agent) {
	seen := map[string]bool{}
	var joined []string
	for _, info := range all {
		if info.PaneID == c.self {
			continue
		}
		seen[info.PaneID] = true
		a := c.agents[info.PaneID]
		if a == nil {
			a = &agent{}
			c.agents[info.PaneID] = a
		}
		previous := a.info
		a.info, a.waiting = info, ""
		if a.member {
			switch {
			case !c.wanted(a):
				c.note(previous.Handle() + " left")
				a.member = false
				a.replacePending("")
			case previous.Name != "" && info.Name == "":
				// Herdr clears the name when an agent loses its context.
				a.member = false
				a.replacePending("")
			case previous.Handle() != info.Handle():
				c.note(previous.Handle() + " is now " + info.Handle())
			}
		}
		ready := info.Status == "idle" || info.Status == "done" || info.Status == "working"
		if !a.member && c.wanted(a) && ready {
			a.member = true
			joined = append(joined, info.PaneID)
			a.replacePending(welcomeFor(info.PaneID, c.self, c.recap()))
			c.note(info.Handle() + " joined")
		}
	}
	for pane, a := range c.agents {
		if !seen[pane] {
			if a.member {
				c.note(a.info.Handle() + " left")
			}
			delete(c.agents, pane)
		}
	}
	for _, pane := range joined {
		c.assignColor(pane)
	}
}

// add either changes membership now or returns a command that starts an agent.
func (c *chat) add(arg string) tea.Cmd {
	for _, a := range c.agents {
		if a.info.Handle() == arg && !a.member {
			a.membership = included
			c.note("adding " + arg)
			return nil
		}
	}
	taken := map[string]bool{}
	for _, a := range c.agents {
		taken[a.info.Name] = true
	}
	name, self := freeName(arg, taken), c.self
	return func() tea.Msg {
		if err := spawn(self, arg, name); err != nil {
			return doneMsg("could not start " + arg + ": " + err.Error())
		}
		return doneMsg("started " + arg + "; it joins once it is ready")
	}
}

func (c *chat) kick(name string) tea.Cmd {
	for _, a := range c.agents {
		if a.member && a.info.Handle() == name {
			a.member, a.membership = false, excluded
			a.replacePending(fmt.Sprintf(leaveText, human, c.self))
			c.note(name + " left the room")
			return nil
		}
	}
	c.note("no one in the room goes by " + name)
	return nil
}

// spawn starts an agent of kind in a new pane beside the largest pane of the
// tab, with that pane's working directory.
func spawn(self, kind, name string) error {
	rects, err := herdr.Layout(self)
	if err != nil {
		return err
	}
	target := herdr.Rect{PaneID: self}
	for _, r := range rects {
		if r.PaneID != self && r.Width*r.Height > target.Width*target.Height {
			target = r
		}
	}
	// A cell is about twice as tall as it is wide.
	direction := "down"
	if target.Width >= 2*target.Height {
		direction = "right"
	}
	cwd := ""
	if panes, err := herdr.Panes(); err == nil {
		for _, p := range panes {
			if p.PaneID == target.PaneID {
				cwd = p.Cwd
			}
		}
	}
	pane, err := herdr.Split(target.PaneID, direction, cwd)
	if err != nil {
		return err
	}
	// The new shell takes a moment to reach its prompt.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(250 * time.Millisecond) {
		err = herdr.Start(name, kind, pane)
		if herdr.Code(err) != "agent_pane_busy" || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		if closeErr := herdr.ClosePane(pane); closeErr != nil {
			return fmt.Errorf("%w; could not close new pane %s: %v", err, pane, closeErr)
		}
		if herdr.Code(err) == "unsupported_agent_kind" {
			return fmt.Errorf("no agent kind or live agent outside the room goes by %s", kind)
		}
	}
	return err
}
