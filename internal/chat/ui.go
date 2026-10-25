package chat

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
		{"help", "", "list commands and keys", func(*chat, string) string { return helpText() }, nil},
	}
}

func helpText() string {
	rows := [][2]string{}
	for _, cmd := range commands {
		rows = append(rows, [2]string{"/" + strings.TrimSpace(cmd.name+" "+cmd.args), cmd.help})
	}
	rows = append(rows,
		[2]string{"@name", "send only to that agent; its reply stays in the chat"},
		[2]string{"tab", "complete a name or command"},
		[2]string{"up / down", "recall sent messages"},
		[2]string{"pgup / pgdn", "scroll the chat; so does the mouse wheel"},
		[2]string{"shift+enter", "new line (also alt+enter or ctrl+j)"},
		[2]string{"//text", "send text that starts with /"})
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = fmt.Sprintf("%-20s %s", r[0], r[1])
	}
	return strings.Join(lines, "\n")
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
	slices.Sort(names)
	slices.Sort(kinds)
	return append(names, kinds...)
}

// entry is one block of the history: a message under its heading, or a note
// when head is empty.
type entry struct{ head, text string }

// render is the entry word-wrapped to width.
func (e entry) render(width int) string {
	if e.head == "" {
		indent := strings.Repeat(" ", min(2, max(width-1, 0)))
		lines := strings.Split(lipgloss.Wrap(e.text, max(width-len(indent), 1), ""), "\n")
		for i, l := range lines {
			lines[i] = faint.Render(indent + l)
		}
		return strings.Join(lines, "\n")
	}
	return "\n" + lipgloss.Wrap(e.head, width, "") + "\n" + lipgloss.Wrap(e.text, width, "")
}

// plainText keeps message contents from moving the terminal cursor or
// changing its styles. Normalize line endings before wrapping to cell width.
func plainText(text string) string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", "    ").Replace(ansi.Strip(text))
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, text))
}

// logSize is how many entries the history keeps.
const logSize = 1000

type model struct {
	c       *chat
	width   int
	height  int
	log     []entry        // the history, oldest first
	history viewport.Model // log, wrapped to the width and scrolled
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
	in.Placeholder = "message the room · @name directs · /help"
	in.ShowLineNumbers = false
	in.DynamicHeight = true
	in.MaxHeight = 8
	in.MaxContentHeight = 10000
	in.KeyMap.InsertNewline.SetKeys("shift+enter", "alt+enter", "ctrl+j")
	in.KeyMap.LineStart.SetKeys("home", "ctrl+a", "meta+left")
	in.KeyMap.LineEnd.SetKeys("end", "ctrl+e", "meta+right")
	styles := textarea.DefaultDarkStyles()
	styles.Focused.CursorLine = lipgloss.NewStyle()
	in.SetStyles(styles)
	in.SetVirtualCursor(false)
	in.Focus()
	history := viewport.New()
	history.KeyMap = viewport.KeyMap{
		PageUp:   key.NewBinding(key.WithKeys("pgup")),
		PageDown: key.NewBinding(key.WithKeys("pgdown")),
	}
	return model{c: c, input: in, history: history}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	return next.layout(), cmd
}

func (m model) update(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case message:
		if e := m.c.receive(msg); e != nil {
			m = m.add(*e)
		}
		return m, nil
	case printMsg:
		return m.add(entry(msg)), nil
	case doneMsg:
		m.pending = ""
		return m.add(entry{text: string(msg)}), nil
	case rosterMsg:
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.MouseWheelMsg:
		m.history, _ = m.history.Update(msg)
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "pgup", "pgdown":
			m.history, _ = m.history.Update(msg)
			return m, nil
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

// add appends e to the history.
func (m model) add(e entry) model {
	m.log = append(m.log, e)
	if len(m.log) > logSize {
		m.log = slices.Clone(m.log[len(m.log)-logSize:])
	}
	return m.refill(m.history.AtBottom())
}

// layout gives the history what the status line and the input box leave.
func (m model) layout() model {
	if m.width < 1 || m.height < 1 {
		return m
	}
	// Leave the terminal's final column free so wrapped text never touches
	// its auto-wrap boundary. Every component uses this same content width.
	width := max(m.width-1, 1)
	m.input.MaxHeight = max(1, min(8, m.height-2))
	m.input.SetWidth(width)
	height := max(m.height-1-m.input.Height(), 0)
	if height == m.history.Height() && width == m.history.Width() {
		return m
	}
	bottom := m.history.AtBottom()
	m.history.SetWidth(width)
	m.history.SetHeight(height)
	return m.refill(bottom)
}

// refill wraps the history to the width, padded so a short one sits just above
// the input box, and keeps it at the bottom if it was there.
func (m model) refill(bottom bool) model {
	if m.history.Width() == 0 {
		return m
	}
	var lines []string
	for _, e := range m.log {
		lines = append(lines, strings.Split(e.render(m.history.Width()), "\n")...)
	}
	if pad := m.history.Height() - len(lines); pad > 0 {
		lines = append(make([]string, pad), lines...)
	}
	m.history.SetContentLines(lines)
	if bottom {
		m.history.GotoBottom()
	}
	return m
}

// submit sends the input box as a message, or runs it as a command.
func (m model) submit() (model, tea.Cmd) {
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
		if e := m.c.receive(message{author: human, text: strings.TrimPrefix(text, "/")}); e != nil {
			m = m.add(*e)
		}
		return m, nil
	}
	name, arg, _ := strings.Cut(text[1:], " ")
	arg = strings.TrimSpace(arg)
	for _, cmd := range commands {
		if cmd.name != name {
			continue
		}
		if cmd.args != "" && arg == "" {
			return m.add(entry{text: "usage: /" + cmd.name + " " + cmd.args}), nil
		}
		m.input.Reset()
		m.pending = "/" + strings.TrimSpace(name+" "+arg) + " …"
		c := m.c
		return m, func() tea.Msg { return doneMsg(cmd.run(c, arg)) }
	}
	return m.add(entry{text: "no command /" + name + "; /help lists them"}), nil
}

func (m model) View() tea.View {
	if m.width < 1 || m.height < 1 {
		return tea.NewView("")
	}
	top := m.statusLine()
	if c, ok := m.completion(); ok {
		top = suggestLine(c)
	} else if m.pending != "" {
		top = faint.Render(m.pending)
	}
	top = ansi.Truncate(top, max(m.width-1, 1), "…")
	var parts []string
	if m.history.Height() > 0 {
		parts = append(parts, m.history.View())
	}
	if m.height > 1 {
		parts = append(parts, top)
	}
	parts = append(parts, lipgloss.NewStyle().MaxWidth(m.width).Render(m.input.View()))
	v := tea.NewView(strings.Join(parts, "\n"))
	v.Cursor = m.input.Cursor()
	if v.Cursor != nil {
		v.Cursor.Position.Y += m.height - m.input.Height()
		if v.Cursor.Position.X >= m.width {
			v.Cursor = nil
		}
	}
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
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
		blurb := ""
		if b := c.blurb[o]; b != "" {
			blurb = " " + b
		}
		parts[i] = faint.Render(o + blurb)
		if i == 0 {
			parts[i] = lipgloss.NewStyle().Bold(true).Render(o) + faint.Render(blurb+" ⇥")
		}
	}
	return strings.Join(parts, "  ")
}

var faint = lipgloss.NewStyle().Faint(true)

// palette is the members' name colors; the human's is green.
var palette = []string{"6", "5", "3", "4", "1"}

// giveColor gives the member in pane a palette color: the one it had if no
// other member holds it, else one the fewest members hold. c.mu is held.
func (c *chat) giveColor(pane string) {
	held := make([]int, len(palette))
	for p := range c.room {
		if i, ok := c.colors[p]; ok && p != pane {
			held[i]++
		}
	}
	if i, ok := c.colors[pane]; ok && held[i] == 0 {
		return
	}
	c.colors[pane] = 0
	for i := range held {
		if held[i] < held[c.colors[pane]] {
			c.colors[pane] = i
		}
	}
}

// nameStyle is how author's name shows: the human in green, a member in its
// color, anyone else plain bold. c.mu is held.
func (c *chat) nameStyle(author, pane string) lipgloss.Style {
	bold := lipgloss.NewStyle().Bold(true)
	if pane == "" && author == human {
		return bold.Foreground(lipgloss.Color("2"))
	}
	if i, ok := c.colors[pane]; ok {
		return bold.Foreground(lipgloss.Color(palette[i]))
	}
	return bold
}

// highlight paints each @mention of a member in that member's color.
func (c *chat) highlight(text string) string {
	c.mu.Lock()
	styles := map[string]lipgloss.Style{}
	for _, a := range c.members() {
		styles[a.Handle()] = c.nameStyle(a.Handle(), a.PaneID)
	}
	c.mu.Unlock()
	return mentionRe.ReplaceAllStringFunc(text, func(m string) string {
		lead, name, _ := strings.Cut(m, "@")
		style, in := styles[name]
		if !in {
			return m
		}
		return lead + style.Render("@"+name)
	})
}

// statusLine lists members, their state, and any refused deliveries.
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
		parts[i] = dot(a) + " " + c.nameStyle(a.Handle(), a.PaneID).Render(a.Handle())
		if n, why := c.queued(a.PaneID); n > 0 {
			parts[i] += faint.Render(fmt.Sprintf(" %d queued, %s", n, why))
		}
	}
	return strings.Join(parts, "  ")
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
