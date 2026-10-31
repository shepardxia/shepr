package chat

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"time"
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
	run              func(c *chat, arg string) tea.Cmd // runs on the UI loop; returns any I/O work
	complete         func(c *chat) []string            // what arg can be
}

var commands []command

func init() {
	commands = []command{
		{"add", "<kind|name>", "start an agent of a kind here, or bring in a live one by name", (*chat).add, (*chat).addable},
		{"kick", "<name>", "take an agent out of the room; it keeps running", (*chat).kick, (*chat).memberNames},
		{"help", "", "list commands and keys", func(c *chat, _ string) tea.Cmd { c.note(helpText()); return nil }, nil},
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
		[2]string{"up / down", "move through input; recall sent messages at its first / last line"},
		[2]string{"pgup / pgdn", "scroll the chat"},
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
	for _, a := range c.members() {
		out = append(out, a.Handle())
	}
	return out
}

// addable is what /add takes: live agents outside the room, then the kinds of
// agent running anywhere.
func (c *chat) addable() []string {
	var names, kinds []string
	seen := map[string]bool{}
	for _, a := range c.agents {
		if !a.member {
			names = append(names, a.info.Handle())
		}
		if !seen[a.info.Kind] {
			seen[a.info.Kind] = true
			kinds = append(kinds, a.info.Kind)
		}
	}
	slices.Sort(names)
	slices.Sort(kinds)
	return append(names, kinds...)
}

// render derives a wrapped display block from a transcript record.
func (c *chat) render(msg message, width int) string {
	if msg.audience == noteAudience {
		indent := strings.Repeat(" ", min(2, max(width-1, 0)))
		lines := strings.Split(lipgloss.Wrap(msg.text, max(width-len(indent), 1), ""), "\n")
		for i, line := range lines {
			lines[i] = faint.Render(indent + line)
		}
		return strings.Join(lines, "\n")
	}
	head := nameStyle(msg.author, msg.pane).Render(msg.author) + "  " + faint.Render(msg.at.Format("15:04"))
	return "\n" + lipgloss.Wrap(head, width, "") + "\n" + lipgloss.Wrap(c.highlight(msg.text), width, "")
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

type model struct {
	c       *chat
	width   int
	height  int
	history viewport.Model // a wrapped, scrollable view of the transcript
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

func (m model) Init() tea.Cmd { return pollRoster }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	revision := m.c.revision
	next, cmd := m.update(msg)
	next = next.layout(next.c.revision != revision)
	return next, tea.Batch(append(next.c.dispatch(), cmd)...)
}

func (m model) update(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case message:
		if err := m.c.receive(msg); err != nil {
			m.c.note(err.Error())
		}
		return m, nil
	case postMsg:
		problem := "your agent is not in this room"
		if msg.post.Pane != "" {
			incoming := message{pane: msg.post.Pane, text: msg.post.Text}
			if msg.post.Local {
				incoming.audience = localAudience
			}
			if err := m.c.receive(incoming); err != nil {
				problem = err.Error()
			} else {
				problem = ""
			}
		}
		msg.reply <- problem
		return m, nil
	case pollMsg:
		return m, pollRoster
	case rosterMsg:
		if msg.err == nil {
			m.c.syncRoster(msg.agents)
		}
		return m, tea.Tick(pollGap, func(time.Time) tea.Msg { return pollMsg{} })
	case deliveredMsg:
		m.c.delivered(msg)
		return m, nil
	case doneMsg:
		m.pending = ""
		m.c.note(string(msg))
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
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
			// Line counts explicit newlines; RowOffset also accounts for
			// wrapping, so a long paragraph still gets normal cursor movement.
			if m.input.Line() == 0 && m.input.LineInfo().RowOffset == 0 && m.recall > 0 {
				if m.recall == len(m.sent) {
					m.draft = m.input.Value()
				}
				m.recall--
				m.input.SetValue(m.sent[m.recall])
				return m, nil
			}
		case "down":
			line := m.input.LineInfo()
			if m.input.Line() == m.input.LineCount()-1 && line.RowOffset == line.Height-1 && m.recall < len(m.sent) {
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

// layout gives the transcript what the status line and input box leave.
func (m model) layout(refresh bool) model {
	if m.width < 1 || m.height < 1 {
		return m
	}
	// Leave the final column free to avoid terminal auto-wrap.
	width := max(m.width-1, 1)
	maxHeight := max(1, min(8, m.height-2))
	if width != m.history.Width() || maxHeight != m.input.MaxHeight {
		m.input.MaxHeight = maxHeight
		m.input.SetWidth(width)
	}
	height := max(m.height-1-m.input.Height(), 0)
	bottom := m.history.AtBottom()
	if height != m.history.Height() || width != m.history.Width() {
		m.history.SetWidth(width)
		m.history.SetHeight(height)
		refresh = true
	}
	if refresh {
		return m.refill(bottom)
	}
	return m
}

// refill wraps the history to the width, padded so a short one sits just above
// the input box, and keeps it at the bottom if it was there.
func (m model) refill(bottom bool) model {
	if m.history.Width() == 0 {
		return m
	}
	var lines []string
	for _, msg := range m.c.transcript {
		lines = append(lines, strings.Split(m.c.render(msg, m.history.Width()), "\n")...)
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
		if err := m.c.receive(message{author: human, text: strings.TrimPrefix(text, "/")}); err != nil {
			m.c.note(err.Error())
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
			m.c.note("usage: /" + cmd.name + " " + cmd.args)
			return m, nil
		}
		if name == "add" && m.pending != "" {
			m.c.note("an agent is already starting")
			return m, nil
		}
		m.input.Reset()
		work := cmd.run(m.c, arg)
		if work != nil {
			m.pending = "/" + strings.TrimSpace(name+" "+arg) + " …"
		}
		return m, work
	}
	m.c.note("no command /" + name + "; /help lists them")
	return m, nil
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
	// Leave mouse input to herdr for native text selection.
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

// Pane identity gives names a stable color, including after an agent leaves.
func nameStyle(author, pane string) lipgloss.Style {
	bold := lipgloss.NewStyle().Bold(true)
	if pane == "" {
		if author == human {
			return bold.Foreground(lipgloss.Color("2"))
		}
		return bold
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(pane))
	return bold.Foreground(lipgloss.Color(palette[int(h.Sum32())%len(palette)]))
}

// highlight paints each @mention of a member in that member's color.
func (c *chat) highlight(text string) string {
	styles := map[string]lipgloss.Style{}
	for _, a := range c.members() {
		styles[a.Handle()] = nameStyle(a.Handle(), a.PaneID)
	}
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
	ms := c.members()
	if len(ms) == 0 {
		return faint.Render("no agents in the room yet · /add <kind> starts one")
	}
	parts := make([]string, len(ms))
	for i, a := range ms {
		parts[i] = dot(a) + " " + nameStyle(a.Handle(), a.PaneID).Render(a.Handle())
		if state := c.agents[a.PaneID]; state.waiting != "" && len(state.pending) > 0 {
			parts[i] += faint.Render(fmt.Sprintf(" %d queued, %s", len(state.pending), state.waiting))
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
