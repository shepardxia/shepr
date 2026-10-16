// Package chat is the chat pane: it shows every message, takes the human's
// from an input box and the agents' from herdr pane send-text, and delivers
// each to every other agent in the tab.
package chat

import (
	"errors"
	"hash/fnv"
	"io"
	"os"
	"os/user"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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

// chat is the state the input box, the delivery loop and the roster poll share.
type chat struct {
	tab, self string
	queue     chan message // shown and delivered one at a time, in order

	mu      sync.Mutex
	members []herdr.Agent
}

// Run is the chat pane for the tab in HERDR_TAB_ID.
func Run() error {
	c := &chat{tab: os.Getenv("HERDR_TAB_ID"), self: os.Getenv("HERDR_PANE_ID"), queue: make(chan message, 64)}
	if c.tab == "" || c.self == "" {
		return errors.New("shepr chat runs as a herdr plugin pane")
	}
	in := terminal{splitInput(os.Stdin, c.member, func(author, pane, text string) {
		c.queue <- message{author, pane, text}
	}), os.Stdin}
	p := tea.NewProgram(newModel(c), tea.WithInput(in))
	go c.deliver(p)
	go c.watch(p)
	_, err := p.Run()
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

// deliver shows each queued message and sends it to every agent in the tab but
// its author, as "author: text".
func (c *chat) deliver(p *tea.Program) {
	for msg := range c.queue {
		p.Send(printMsg("\n" + byline(msg.author) + "\n" + msg.text))
		var wg sync.WaitGroup
		var mu sync.Mutex
		var missed []string
		for _, a := range c.roster() {
			if a.PaneID == msg.pane {
				continue
			}
			wg.Add(1)
			go func(a herdr.Agent) {
				defer wg.Done()
				if err := herdr.Prompt(a.PaneID, msg.author+": "+msg.text); err != nil {
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
			p.Send(printMsg(faint.Render("  not delivered to " + strings.Join(missed, ", "))))
		}
	}
}

// watch keeps the roster current and welcomes each agent that joins. An
// unnamed agent is always new: every welcomed agent holds a herdr name, and
// herdr clears it when the agent exits.
func (c *chat) watch(p *tea.Program) {
	welcomed := map[string]bool{}
	for ; ; time.Sleep(2 * time.Second) {
		all, err := herdr.Agents()
		if err != nil {
			continue
		}
		ms := c.setMembers(all)
		p.Send(rosterMsg{})
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

// setMembers keeps the agents of this tab out of all and returns them.
func (c *chat) setMembers(all []herdr.Agent) []herdr.Agent {
	var ms []herdr.Agent
	for _, a := range all {
		if a.TabID == c.tab && a.PaneID != c.self {
			ms = append(ms, a)
		}
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].PaneID < ms[j].PaneID })
	c.mu.Lock()
	c.members = ms
	c.mu.Unlock()
	return ms
}

func (c *chat) roster() []herdr.Agent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.members
}

// member resolves name to the pane of an agent in this tab, asking herdr
// afresh on a miss so a message sent right after a rename is taken and not
// delivered back to its author.
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
	return find(c.setMembers(all))
}

type (
	printMsg  string   // a message to print above the input box
	rosterMsg struct{} // the roster changed
)

type model struct {
	c     *chat
	input textarea.Model
}

func newModel(c *chat) model {
	in := textarea.New()
	in.Prompt = "› "
	in.ShowLineNumbers = false
	in.DynamicHeight = true
	in.MaxHeight = 8
	in.MaxContentHeight = 10000
	in.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")
	in.KeyMap.LineStart.SetKeys("home", "ctrl+a", "meta+left")
	in.KeyMap.LineEnd.SetKeys("end", "ctrl+e", "meta+right")
	styles := textarea.DefaultDarkStyles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	in.SetStyles(styles)
	in.Focus()
	return model{c: c, input: in}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case printMsg:
		return m, tea.Println(string(msg))
	case rosterMsg:
		return m, nil
	case tea.WindowSizeMsg:
		m.input.SetWidth(msg.Width)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+d":
			if m.input.Value() == "" {
				return m, tea.Quit
			}
		case "enter":
			if text := strings.TrimSpace(m.input.Value()); text != "" {
				m.c.queue <- message{author: human, text: text}
			}
			m.input.Reset()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() tea.View {
	return tea.NewView(rosterLine(m.c.roster()) + "\n" + m.input.View())
}

var faint = lipgloss.NewStyle().Faint(true)

// byline is a message's heading: the human in green, each agent in a bold
// color of its own, then the time.
func byline(author string) string {
	color := "2"
	if author != human {
		h := fnv.New32a()
		h.Write([]byte(author))
		color = []string{"6", "5", "3", "4", "1"}[h.Sum32()%5]
	}
	name := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Render(author)
	return name + "  " + faint.Render(time.Now().Format("15:04"))
}

// rosterLine lists the agents in the tab, each with a dot for its state.
func rosterLine(ms []herdr.Agent) string {
	if len(ms) == 0 {
		return faint.Render("no agents in this tab")
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
	return strings.Join(parts, "  ")
}
