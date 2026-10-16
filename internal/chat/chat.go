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
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

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

// held is a message that @mentions agents. It went to them first; the rest of
// the tab gets it together with their replies.
type held struct {
	parts   []message
	first   map[string]bool // panes the opening message already reached
	waiting map[string]bool // mentioned panes yet to reply in the chat
}

// deliver shows each queued message and sends it on as "author: text". A
// message that @mentions agents goes to them alone and is held for the rest
// until they all reply, or until anything else is said.
func (c *chat) deliver(p *tea.Program) {
	var h *held
	for msg := range c.queue {
		p.Send(printMsg("\n" + byline(msg.author) + "\n" + msg.text))
		if h != nil && h.waiting[msg.pane] {
			h.parts = append(h.parts, msg)
			delete(h.waiting, msg.pane)
			if len(h.waiting) == 0 {
				c.release(p, h)
				h = nil
			}
			continue
		}
		if h != nil {
			c.release(p, h)
			h = nil
		}
		mentioned := c.mentioned(msg)
		if len(mentioned) == 0 {
			c.send(p, c.roster(), func(a herdr.Agent) string { return unseen(a, []message{msg}, nil) })
			continue
		}
		first := map[string]bool{}
		for _, a := range mentioned {
			first[a.PaneID] = true
		}
		reached := c.send(p, mentioned, func(herdr.Agent) string { return msg.author + ": " + msg.text })
		h = &held{parts: []message{msg}, first: first, waiting: reached}
		if len(reached) == 0 {
			c.release(p, h)
			h = nil
			continue
		}
		var names []string
		for _, a := range mentioned {
			if reached[a.PaneID] {
				names = append(names, a.Handle())
			}
		}
		p.Send(printMsg(faint.Render("  the rest wait for " + strings.Join(names, ", "))))
	}
}

// release sends a held message and its replies to every agent, each getting
// the parts it has not seen.
func (c *chat) release(p *tea.Program, h *held) {
	c.send(p, c.roster(), func(a herdr.Agent) string { return unseen(a, h.parts, h.first) })
}

// unseen is what agent a has not yet seen of parts: none of its own, and not
// the opening part if it was among first.
func unseen(a herdr.Agent, parts []message, first map[string]bool) string {
	var out []string
	for i, m := range parts {
		if m.pane == a.PaneID || (i == 0 && first[a.PaneID]) {
			continue
		}
		out = append(out, m.author+": "+m.text)
	}
	return strings.Join(out, "\n\n")
}

var mentionRe = regexp.MustCompile(`(?:^|\s)@([a-z][a-z0-9_-]{0,31})`)

// mentioned is the agents in the tab that msg @mentions, other than its author.
func (c *chat) mentioned(msg message) []herdr.Agent {
	names := map[string]bool{}
	for _, m := range mentionRe.FindAllStringSubmatch(msg.text, -1) {
		names[m[1]] = true
	}
	var out []herdr.Agent
	for _, a := range c.roster() {
		if names[a.Handle()] && a.PaneID != msg.pane {
			out = append(out, a)
		}
	}
	return out
}

// send prompts each agent in to with text(agent), skipping empty texts, and
// returns the panes it reached. Agents it could not reach are noted in the chat.
func (c *chat) send(p *tea.Program, to []herdr.Agent, text func(herdr.Agent) string) map[string]bool {
	var wg sync.WaitGroup
	var mu sync.Mutex
	reached := map[string]bool{}
	var missed []string
	for _, a := range to {
		t := text(a)
		if t == "" {
			continue
		}
		wg.Add(1)
		go func(a herdr.Agent) {
			defer wg.Done()
			err := herdr.Prompt(a.PaneID, t)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				reached[a.PaneID] = true
				return
			}
			reason := err.Error()
			var he *herdr.Error
			if errors.As(err, &he) {
				reason = strings.TrimPrefix(he.Code, "agent_")
			}
			missed = append(missed, a.Handle()+" ("+reason+")")
		}(a)
	}
	wg.Wait()
	if len(missed) > 0 {
		sort.Strings(missed)
		p.Send(printMsg(faint.Render("  not delivered to " + strings.Join(missed, ", "))))
	}
	return reached
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
		case "tab":
			if typed, ok := typedMention(m.input); ok {
				if names := m.matches(typed); len(names) > 0 {
					m.input.InsertString(names[0][len(typed):] + " ")
				}
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() tea.View {
	top := rosterLine(m.c.roster())
	if typed, ok := typedMention(m.input); ok {
		top = suggestLine(m.matches(typed))
	}
	return tea.NewView(top + "\n" + m.input.View())
}

// typedMention is the @name being typed at the cursor, without the @.
func typedMention(in textarea.Model) (string, bool) {
	line := []rune(strings.Split(in.Value(), "\n")[in.Line()])
	col := min(in.Column(), len(line))
	if col < len(line) && !unicode.IsSpace(line[col]) {
		return "", false
	}
	start := col
	for start > 0 && !unicode.IsSpace(line[start-1]) {
		start--
	}
	word := string(line[start:col])
	if !strings.HasPrefix(word, "@") {
		return "", false
	}
	return word[1:], true
}

// matches is the agents in the tab whose names start with prefix.
func (m model) matches(prefix string) []string {
	var names []string
	for _, a := range m.c.roster() {
		if strings.HasPrefix(a.Handle(), prefix) {
			names = append(names, a.Handle())
		}
	}
	return names
}

// suggestLine shows the names an @ could complete to, the one Tab picks first.
func suggestLine(names []string) string {
	if len(names) == 0 {
		return faint.Render("no agent by that name")
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = faint.Render("@" + n)
	}
	parts[0] = lipgloss.NewStyle().Bold(true).Render("@"+names[0]) + faint.Render(" tab")
	return strings.Join(parts, "  ")
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
