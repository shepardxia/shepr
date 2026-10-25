package chat

import (
	"errors"
	"fmt"
	"time"

	"github.com/shepardxia/shepr/internal/herdr"
)

// pollGap is how often the room is checked against herdr's agents.
const pollGap = 2 * time.Second

// watch keeps the room current: it welcomes each agent that joins, and
// notes those that leave or rename. Every member holds a herdr name, so one
// whose name herdr cleared, as it does when an agent exits or compacts its
// context, leaves and is welcomed back.
func (c *chat) watch() {
	last, joined := map[string]string{}, map[string]bool{}
	tick := time.NewTicker(pollGap)
	for {
		if all, err := herdr.Agents(); err == nil {
			var joining []herdr.Agent
			c.act(func() (out []delivery, notes []string) {
				joining, out, notes = c.update(all)
				return out, notes
			})
			for _, a := range joining {
				if name, err := c.join(a, last[a.PaneID], all); err == nil {
					verb := " joined"
					if joined[a.PaneID] {
						verb = " is back"
					}
					joined[a.PaneID] = true
					c.note(name + verb)
				}
			}
			for _, a := range all {
				if a.Name != "" {
					last[a.PaneID] = a.Name
				}
			}
			c.redraw()
		}
		select {
		case <-tick.C:
		case <-c.poke:
		}
	}
}

// update takes in herdr's live agents: members that left the room or lost
// their name drop out, renames are noted, and holds waiting on a finished turn
// settle. It returns the agents ready to join. c.mu is held.
func (c *chat) update(all []herdr.Agent) (joining []herdr.Agent, out []delivery, notes []string) {
	c.live = all
	byPane := map[string]herdr.Agent{}
	for _, a := range all {
		byPane[a.PaneID] = a
	}
	for pane := range c.kicked {
		if _, ok := byPane[pane]; !ok {
			delete(c.kicked, pane)
		}
	}
	for pane := range c.invited {
		if _, ok := byPane[pane]; !ok {
			delete(c.invited, pane)
		}
	}
	want := func(a herdr.Agent) bool {
		return a.PaneID != c.self && !c.kicked[a.PaneID] && (a.TabID == c.tab || c.invited[a.PaneID])
	}
	for _, m := range c.members() {
		a, ok := byPane[m.PaneID]
		switch {
		case !ok || !want(a):
			delete(c.room, m.PaneID)
			notes = append(notes, m.Handle()+" left")
		case a.Name == "":
			delete(c.room, m.PaneID)
		default:
			if a.Name != m.Name {
				notes = append(notes, m.Handle()+" is now "+a.Name)
				c.names[a.Name] = m.PaneID
			}
			c.room[m.PaneID] = a
		}
	}
	for _, b := range c.boxes {
		b.signal()
	}
	for _, a := range all {
		_, in := c.room[a.PaneID]
		ready := a.Status == "idle" || a.Status == "done" || a.Status == "working"
		if !in && ready && want(a) {
			joining = append(joining, a)
		}
	}
	out, settled := c.settle()
	return joining, out, append(notes, settled...)
}

// join names an agent if herdr has not, makes it a member, welcomes it with a
// recap of the chat so far, and returns its name. An unnamed agent first gets
// back last, the name its pane last held, or else a placeholder herdr name, so
// the welcome's rename command can name it rather than its pane id.
func (c *chat) join(a herdr.Agent, last string, all []herdr.Agent) (string, error) {
	if a.Name == "" && last != "" && herdr.Rename(a.PaneID, last) == nil {
		a.Name = last
	}
	if a.Name == "" {
		a.Name = freeName(a.Kind, takenNames(all))
		if err := herdr.Rename(a.PaneID, a.Name); err != nil {
			return "", err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kicked[a.PaneID] {
		return "", errors.New("removed while joining")
	}
	c.room[a.PaneID] = a
	c.names[a.Name] = a.PaneID
	c.giveColor(a.PaneID)
	c.box(a.PaneID).reset(welcomeFor(a.Name, c.self, c.history))
	return a.Name, nil
}

func takenNames(all []herdr.Agent) map[string]bool {
	taken := map[string]bool{}
	for _, a := range all {
		taken[a.Name] = true
	}
	return taken
}

// add brings arg into the room: a live agent outside it by name, or else a
// new agent of kind arg, started in a pane of this tab.
func (c *chat) add(arg string) string {
	c.mu.Lock()
	for _, a := range c.live {
		if _, in := c.room[a.PaneID]; a.Name != arg || in || a.PaneID == c.self {
			continue
		}
		delete(c.kicked, a.PaneID)
		if a.TabID != c.tab {
			c.invited[a.PaneID] = true
		}
		c.mu.Unlock()
		c.pokeWatch()
		return "adding " + arg
	}
	taken := takenNames(c.live)
	c.mu.Unlock()
	if err := c.spawn(arg, taken); err != nil {
		return "could not start " + arg + ": " + err.Error()
	}
	c.pokeWatch()
	return "started " + arg + "; it joins once it is ready"
}

// spawn starts an agent of kind in a new pane beside the largest pane of the
// tab, with that pane's working directory.
func (c *chat) spawn(kind string, taken map[string]bool) error {
	name := freeName(kind, taken)
	// herdr checks the kind first, and the chat pane is never a free shell,
	// so this starts nothing and fails on a kind herdr cannot start.
	if err := herdr.Start(name, kind, c.self); herdr.Code(err) == "unsupported_agent_kind" {
		return fmt.Errorf("no agent kind or live agent outside the room goes by %s", kind)
	}
	rects, err := herdr.Layout(c.self)
	if err != nil {
		return err
	}
	target := herdr.Rect{PaneID: c.self}
	for _, r := range rects {
		if r.PaneID != c.self && r.Width*r.Height > target.Width*target.Height {
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
		_ = herdr.ClosePane(pane)
	}
	return err
}

// kick takes the member that goes by name out of the room and tells it so.
// It stays in its pane, and add brings it back.
func (c *chat) kick(name string) string {
	found := false
	c.act(func() ([]delivery, []string) {
		for pane, a := range c.room {
			if a.Handle() != name {
				continue
			}
			found = true
			delete(c.room, pane)
			if c.invited[pane] {
				delete(c.invited, pane)
			} else {
				c.kicked[pane] = true
			}
			c.box(pane).reset(fmt.Sprintf(leaveText, human, c.self))
		}
		return c.settle()
	})
	if !found {
		return "no one in the room goes by " + name
	}
	return name + " left the room"
}

// releaseAll sends every held message on now.
func (c *chat) releaseAll() string {
	n := 0
	c.act(func() ([]delivery, []string) {
		n = len(c.holds)
		for _, h := range c.holds {
			clear(h.waiting)
		}
		return c.settle()
	})
	if n == 0 {
		return "nothing is held"
	}
	return fmt.Sprintf("sent %d held message(s) to everyone", n)
}
