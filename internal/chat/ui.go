package chat

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/shepardxia/shepr/internal/herdr"
)

// command is a slash command typed in the input box.
type command struct {
	name, args, help string
	run              func(c *chat, arg string) string // runs off the program's loop
	complete         func(c *chat) []string           // what arg can be
}

var commands []command

func init() {
	commands = []command{
		{"add", "<kind|name>", "start an agent of a kind here, or bring in a live one by name", (*chat).add, (*chat).addable},
		{"kick", "<name>", "take an agent out of the room; it keeps running", (*chat).kick, (*chat).memberNames},
		{"release", "", "send held @messages to everyone now", func(c *chat, _ string) string { return c.releaseAll() }, nil},
		{"help", "", "list commands and keys", func(*chat, string) string { return helpText() }, nil},
	}
}

func helpText() string {
	var lines []string
	for _, cmd := range commands {
		lines = append(lines, fmt.Sprintf("  %-22s %s", "/"+strings.TrimSpace(cmd.name+" "+cmd.args), cmd.help))
	}
	lines = append(lines,
		fmt.Sprintf("  %-22s %s", "@name", "send to that agent first; the rest get it with its reply"),
		fmt.Sprintf("  %-22s %s", "tab", "complete a name or command"),
		fmt.Sprintf("  %-22s %s", "up / down", "recall sent messages"),
		fmt.Sprintf("  %-22s %s", "alt+enter", "new line"),
		fmt.Sprintf("  %-22s %s", "//text", "send text that starts with /"))
	return faint.Render(strings.Join(lines, "\n"))
}

// memberNames is the members' names.
func (c *chat) memberNames() []string {
	var out []string
	for _, a := range c.roster() {
		out = append(out, a.Handle())
	}
	return out
}

// addable is what /add takes: live agents outside the room, then the kinds of
// agent running anywhere.
func (c *chat) addable() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var names, kinds []string
	seen := map[string]bool{}
	for _, a := range c.live {
		if _, in := c.room[a.PaneID]; a.Name != "" && !in && a.PaneID != c.self {
			names = append(names, a.Name)
		}
		if !seen[a.Kind] {
			seen[a.Kind] = true
			kinds = append(kinds, a.Kind)
		}
	}
	sort.Strings(names)
	sort.Strings(kinds)
	return append(names, kinds...)
}

type model struct {
	c       *chat
	width   int
	input   textarea.Model
	sent    []string // what the human sent, oldest first
	recall  int      // index into sent while recalling, len(sent) otherwise
	draft   string   // the unsent text recalling set aside
	pending string   // a command under way, shown until its result arrives
}

// doneMsg is a command's result.
type doneMsg string

func newModel(c *chat) model {
	in := textarea.New()
	in.Prompt = "› "
	in.Placeholder = "message the room · @name asks first · /help"
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
		return m, m.println(string(msg))
	case doneMsg:
		m.pending = ""
		return m, m.println(string(msg))
	case rosterMsg:
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.input.SetWidth(msg.Width)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			if m.input.Value() == "" {
				return m, tea.Quit
			}
			m.input.Reset()
			return m, nil
		case "ctrl+d":
			if m.input.Value() == "" {
				return m, tea.Quit
			}
		case "enter":
			return m.submit()
		case "tab":
			if c, ok := m.completion(); ok {
				if len(c.options) > 0 {
					m.input.InsertString(c.options[0][len(c.typed):] + " ")
				}
				return m, nil
			}
		case "up":
			if m.input.Line() == 0 && m.recall > 0 {
				if m.recall == len(m.sent) {
					m.draft = m.input.Value()
				}
				m.recall--
				m.input.SetValue(m.sent[m.recall])
				return m, nil
			}
		case "down":
			if m.input.Line() == m.input.LineCount()-1 && m.recall < len(m.sent) {
				m.recall++
				if m.recall == len(m.sent) {
					m.input.SetValue(m.draft)
				} else {
					m.input.SetValue(m.sent[m.recall])
				}
				return m, nil
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// println prints text above the input box, wrapped at word breaks to fit.
func (m model) println(text string) tea.Cmd {
	if m.width > 0 {
		text = lipgloss.Wrap(text, m.width, "")
	}
	return tea.Println(text)
}

// submit sends the input box as a message, or runs it as a command.
func (m model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	if len(m.sent) == 0 || m.sent[len(m.sent)-1] != text {
		m.sent = append(m.sent, text)
	}
	m.recall, m.draft = len(m.sent), ""
	if !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
		m.input.Reset()
		m.c.queue <- message{author: human, text: strings.TrimPrefix(text, "/")}
		return m, nil
	}
	name, arg, _ := strings.Cut(text[1:], " ")
	arg = strings.TrimSpace(arg)
	for _, cmd := range commands {
		if cmd.name != name {
			continue
		}
		if cmd.args != "" && arg == "" {
			return m, m.println(faint.Render("  usage: /" + cmd.name + " " + cmd.args))
		}
		m.input.Reset()
		m.pending = "/" + strings.TrimSpace(name+" "+arg) + " …"
		c := m.c
		return m, func() tea.Msg { return doneMsg(cmd.run(c, arg)) }
	}
	return m, m.println(faint.Render("  no command /" + name + "; /help lists them"))
}

func (m model) View() tea.View {
	top := m.statusLine()
	if c, ok := m.completion(); ok {
		top = suggestLine(c)
	} else if m.pending != "" {
		top = faint.Render(m.pending)
	}
	return tea.NewView(top + "\n" + m.input.View())
}

// completion is what the word at the cursor could become: a member after @, a
// command at the start of the box, or a command's argument after it.
type completion struct {
	typed   string // the part of the word that options extend
	options []string
	blurb   map[string]string // shown beside an option
}

func (m model) completion() (completion, bool) {
	lines := strings.Split(m.input.Value(), "\n")
	line := []rune(lines[m.input.Line()])
	col := min(m.input.Column(), len(line))
	if col < len(line) && !unicode.IsSpace(line[col]) {
		return completion{}, false
	}
	start := col
	for start > 0 && !unicode.IsSpace(line[start-1]) {
		start--
	}
	word := string(line[start:col])
	head := strings.Fields(string(line[:start]))
	switch {
	case strings.HasPrefix(word, "@"):
		return complete(word[1:], m.c.memberNames(), nil), true
	case m.input.Line() == 0 && len(head) == 0 && strings.HasPrefix(word, "/") && !strings.HasPrefix(word, "//"):
		var names []string
		blurb := map[string]string{}
		for _, cmd := range commands {
			names = append(names, cmd.name)
			blurb[cmd.name] = cmd.args
		}
		return complete(word[1:], names, blurb), true
	case m.input.Line() == 0 && len(head) == 1:
		for _, cmd := range commands {
			if head[0] == "/"+cmd.name && cmd.complete != nil {
				return complete(word, cmd.complete(m.c), nil), true
			}
		}
	}
	return completion{}, false
}

func complete(typed string, all []string, blurb map[string]string) completion {
	c := completion{typed: typed, blurb: blurb}
	for _, s := range all {
		if strings.HasPrefix(s, typed) {
			c.options = append(c.options, s)
		}
	}
	return c
}

// suggestLine shows what the word at the cursor could become, the one Tab
// picks first.
func suggestLine(c completion) string {
	if len(c.options) == 0 {
		return faint.Render("no match")
	}
	parts := make([]string, len(c.options))
	for i, o := range c.options {
		parts[i] = faint.Render(strings.TrimSpace(o + " " + c.blurb[o]))
	}
	parts[0] = lipgloss.NewStyle().Bold(true).Render(c.options[0]) + faint.Render(strings.TrimRight(" "+c.blurb[c.options[0]], " ")+" ⇥")
	return strings.Join(parts, "  ")
}

var faint = lipgloss.NewStyle().Faint(true)

// paint is how author's name shows: the human in green, each agent in a bold
// color of its own.
func paint(author string) lipgloss.Style {
	color := "2"
	if author != human {
		h := fnv.New32a()
		h.Write([]byte(author))
		color = []string{"6", "5", "3", "4", "1"}[h.Sum32()%5]
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color))
}

// byline is a message's heading: its author, then the time.
func byline(author string) string {
	return paint(author).Render(author) + "  " + faint.Render(time.Now().Format("15:04"))
}

// highlight paints each @mention of a member in that member's color.
func (c *chat) highlight(text string) string {
	in := map[string]bool{}
	for _, a := range c.roster() {
		in[a.Handle()] = true
	}
	return mentionRe.ReplaceAllStringFunc(text, func(m string) string {
		lead, name, _ := strings.Cut(m, "@")
		if !in[name] {
			return m
		}
		return lead + paint(name).Render("@"+name)
	})
}

// statusLine lists the members, each with a dot for its state and a count of
// what herdr keeps refusing it, then the agents a hold waits on.
func (m model) statusLine() string {
	c := m.c
	c.mu.Lock()
	defer c.mu.Unlock()
	ms := c.members()
	if len(ms) == 0 {
		return faint.Render("no agents in the room yet · /add <kind> starts one")
	}
	parts := make([]string, len(ms))
	for i, a := range ms {
		parts[i] = dot(a) + " " + paint(a.Handle()).Render(a.Handle())
		if n, why := c.queued(a.PaneID); n > 0 {
			parts[i] += faint.Render(fmt.Sprintf(" %d queued, %s", n, why))
		}
	}
	line := strings.Join(parts, "  ")
	var waiting []string
	for _, h := range c.holds {
		for pane := range h.waiting {
			if a, in := c.room[pane]; in {
				waiting = append(waiting, a.Handle())
			}
		}
	}
	if len(waiting) > 0 {
		sort.Strings(waiting)
		line += faint.Render("   ⏸ held for " + strings.Join(dedup(waiting), ", ") + " · /release")
	}
	return line
}

func dot(a herdr.Agent) string {
	switch a.Status {
	case "idle", "done":
		return "●"
	case "working":
		return "◐"
	case "blocked":
		return "○"
	}
	return "·"
}

// dedup drops repeats from a sorted list.
func dedup(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
