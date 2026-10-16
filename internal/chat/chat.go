// Package chat is the chat pane: it shows every message, takes the human's
// from an input line and the agents' from herdr pane send-text, and delivers
// each to every other agent in the tab.
package chat

import (
	"github.com/shepardxia/shepr/internal/herdr"

	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/user"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chzyer/readline"
)

// human is the name the person at the input line posts under: the first name
// on the account, else the login.
var human = func() string {
	if u, err := user.Current(); err == nil {
		if f := strings.Fields(u.Name); len(f) > 0 {
			return f[0]
		}
	}
	return os.Getenv("USER")
}()

// chat is one tab's chat pane.
type chat struct {
	tab, self string
	rl        *readline.Instance
	out       io.Writer

	mu      sync.Mutex // guards members
	members []herdr.Agent

	send sync.Mutex // one message at a time, so every agent sees the same order
}

// Run is the chat pane for the tab in HERDR_TAB_ID.
func Run() error {
	c := &chat{tab: os.Getenv("HERDR_TAB_ID"), self: os.Getenv("HERDR_PANE_ID")}
	if c.tab == "" || c.self == "" {
		return errors.New("shepr chat runs as a herdr plugin pane")
	}
	fromAgents := make(chan [3]string, 16)
	stdin := splitInput(os.Stdin, c.member, func(author, pane, text string) {
		fromAgents <- [3]string{author, pane, text}
	})
	rl, err := readline.NewEx(&readline.Config{Stdin: stdin, Prompt: "› ", UniqueEditLine: true})
	if err != nil {
		return err
	}
	c.rl, c.out = rl, rl.Stdout()

	go c.watch()
	go func() {
		for m := range fromAgents {
			c.post(m[0], m[1], m[2])
		}
	}()

	lines := make(chan string)
	go func() {
		defer close(lines)
		for {
			l, err := rl.Readline()
			if err == readline.ErrInterrupt && l != "" {
				continue
			}
			if err != nil {
				return
			}
			lines <- l
		}
	}()
	for text, ok := pasted(lines); ok; text, ok = pasted(lines) {
		if text = strings.TrimSpace(text); text != "" {
			c.post(human, "", text)
		}
	}
	// readline's Close blocks on its input loop, so leave raw mode and return.
	return rl.Terminal.ExitRawMode()
}

// pasted reads one message: a line plus any lines that follow within a beat,
// which is how a multi-line paste arrives.
func pasted(lines <-chan string) (string, bool) {
	first, ok := <-lines
	if !ok {
		return "", false
	}
	msg := []string{first}
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return strings.Join(msg, "\n"), true
			}
			msg = append(msg, l)
		case <-time.After(40 * time.Millisecond):
			return strings.Join(msg, "\n"), true
		}
	}
}

// post shows a message and delivers it to every agent in the tab but the one
// in pane, its author, as "author: text". The human's messages have no pane.
func (c *chat) post(author, pane, text string) {
	c.send.Lock()
	defer c.send.Unlock()
	fmt.Fprintf(c.out, "\n%s  %s\n%s\n", colorName(author), dim(time.Now().Format("15:04")), text)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var missed []string
	for _, a := range c.roster() {
		if a.PaneID == pane {
			continue
		}
		wg.Add(1)
		go func(a herdr.Agent) {
			defer wg.Done()
			if err := herdr.Prompt(a.PaneID, author+": "+text); err != nil {
				reason := err.Error()
				var he *herdr.Error
				if errors.As(err, &he) {
					reason = strings.TrimPrefix(he.Code, "agent_")
				}
				mu.Lock()
				missed = append(missed, a.Handle()+" ("+reason+")")
				mu.Unlock()
			}
		}(a)
	}
	wg.Wait()
	if len(missed) > 0 {
		sort.Strings(missed)
		fmt.Fprintln(c.out, dim("  not delivered to "+strings.Join(missed, ", ")))
	}
}

// watch keeps the roster current and welcomes each agent that joins. An
// unnamed agent is always new: every welcomed agent holds a herdr name, and
// herdr clears it when the agent exits.
func (c *chat) watch() {
	welcomed := map[string]bool{}
	for ; ; time.Sleep(2 * time.Second) {
		all, err := herdr.Agents()
		if err != nil {
			continue
		}
		ms := c.inTab(all)
		c.mu.Lock()
		c.members = ms
		c.mu.Unlock()
		c.rl.SetPrompt(rosterLine(ms))
		c.rl.Refresh()
		for _, a := range ms {
			if a.Name == "" {
				welcomed[a.PaneID] = false
			}
			ready := a.Status == "idle" || a.Status == "done" || a.Status == "working"
			if !welcomed[a.PaneID] && ready {
				welcomed[a.PaneID] = welcome(a, all, c.self) == nil
			}
		}
	}
}

func (c *chat) inTab(all []herdr.Agent) []herdr.Agent {
	var ms []herdr.Agent
	for _, a := range all {
		if a.TabID == c.tab && a.PaneID != c.self {
			ms = append(ms, a)
		}
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].PaneID < ms[j].PaneID })
	return ms
}

func (c *chat) roster() []herdr.Agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.members
}

// member resolves name to the pane of an agent in this tab. On a miss it asks
// herdr afresh and keeps the answer, so a message sent right after a rename is
// taken and not delivered back to its author.
func (c *chat) member(name string) (string, bool) {
	find := func(ms []herdr.Agent) (string, bool) {
		for _, a := range ms {
			if a.Handle() == name {
				return a.PaneID, true
			}
		}
		return "", false
	}
	if pane, ok := find(c.roster()); ok {
		return pane, true
	}
	all, err := herdr.Agents()
	if err != nil {
		return "", false
	}
	ms := c.inTab(all)
	c.mu.Lock()
	c.members = ms
	c.mu.Unlock()
	return find(ms)
}

var color = os.Getenv("NO_COLOR") == ""

func dim(s string) string {
	if !color {
		return s
	}
	return "\x1b[2m" + s + "\x1b[0m"
}

// colorName shows the human in green and each agent in a bold color of its own.
func colorName(name string) string {
	if !color {
		return name
	}
	code := "32"
	if name != human {
		h := fnv.New32a()
		h.Write([]byte(name))
		code = []string{"36", "35", "33", "34", "31"}[h.Sum32()%5]
	}
	return "\x1b[1;" + code + "m" + name + "\x1b[0m"
}

// rosterLine is the input prompt: each agent with a dot for its state.
func rosterLine(ms []herdr.Agent) string {
	if len(ms) == 0 {
		return "no agents in this tab › "
	}
	parts := make([]string, len(ms))
	for i, a := range ms {
		dot := "·"
		switch a.Status {
		case "idle", "done":
			dot = "●"
		case "working":
			dot = "◐"
		case "blocked":
			dot = "○"
		}
		parts[i] = dot + " " + a.Handle()
	}
	return strings.Join(parts, "  ") + " › "
}
