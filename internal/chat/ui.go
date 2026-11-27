package chat

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/shepardxia/shepr/internal/herdr"
)

// command is a slash command typed in the input box.
type command struct {
	name, args, help string
	run              func(m *model, arg string) tea.Cmd // runs on the UI loop; returns any I/O work
	complete         func(c *chat) []string             // what arg can be
}

var commands []command

func init() {
	commands = []command{
		{"add", "<kind|name>", "start an agent of a kind here, or bring in a live one by name", (*model).add, (*chat).addable},
		{"kick", "<name>", "take an agent out of the room; it keeps running", func(m *model, arg string) tea.Cmd { return m.c.kick(arg) }, (*chat).memberNames},
		{"attach", "<path>", "attach an image to your next message", (*model).attach, nil},
		{"detach", "", "remove the last attached image", (*model).detach, nil},
		{"help", "", "list commands and keys", func(m *model, _ string) tea.Cmd { m.c.note(helpText()); return nil }, nil},
	}
}

func helpText() string {
	rows := [][2]string{}
	for _, cmd := range commands {
		rows = append(rows, [2]string{"/" + strings.TrimSpace(cmd.name+" "+cmd.args), cmd.help})
	}
	rows = append(rows,
		[2]string{"@one @two", "send to each named member once"},
		[2]string{"ctrl+v", "paste an image or text from the clipboard"},
		[2]string{"ctrl+c", "clear text and attachments or quit an empty draft"},
		[2]string{"tab", "complete a name or command"},
		[2]string{"up / down", "move through input; recall sent messages at its first / last line"},
		[2]string{"wheel / pgup / pgdn", "scroll the chat"},
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

type model struct {
	c        *chat
	width    int
	height   int
	input    textarea.Model
	sent     []string // what the human sent, oldest first
	recall   int      // index into sent while recalling, len(sent) otherwise
	draft    string   // the unsent text recalling set aside
	printed  uint64
	printing bool
	images   []attachment
	loading  *pendingPaste
	pending  string // a command under way, shown until its result arrives
}

// doneMsg is a command's result.
type doneMsg string

func newModel(c *chat) model {
	in := textarea.New()
	in.Prompt = "› "
	in.Placeholder = "message the room · @name directs · /attach image · /help"
	in.ShowLineNumbers = false
	in.DynamicHeight = true
	in.MaxWidth = 0
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
	return model{c: c, input: in}
}

func (m model) Init() tea.Cmd { return pollRoster }

type printedMsg struct{}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	output := next.printNew()
	return next, tea.Batch(append(next.c.dispatch(), cmd, output)...)
}

// Print one batch at a time so concurrent posts keep their transcript order.
func (m *model) printNew() tea.Cmd {
	if m.width < 1 || m.printing || m.printed == m.c.revision {
		return nil
	}
	count := min(int(m.c.revision-m.printed), len(m.c.transcript))
	var blocks []string
	for _, msg := range m.c.transcript[len(m.c.transcript)-count:] {
		blocks = append(blocks, m.c.render(msg, max(m.width-1, 1)))
	}
	m.printed, m.printing = m.c.revision, true
	return tea.Sequence(tea.Println(strings.Join(blocks, "\n")), func() tea.Msg { return printedMsg{} })
}

func (m model) update(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case printedMsg:
		m.printing = false
		return m, nil
	case message:
		if err := m.c.receive(msg); err != nil {
			m.c.note(err.Error())
		}
		return m, nil
	case postMsg:
		msg.reply <- m.c.acceptPost(msg.message)
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
		m.input.MaxHeight = max(1, min(8, m.height-1))
		m.input.SetWidth(max(m.width-1, 1))
		return m, nil
	case pasteResult:
		return m.finishPaste(msg)
	case tea.PasteMsg:
		if m.loading != nil {
			m.loading.sendWhenReady = false
		}
		var cmd tea.Cmd
		if path := pastedImagePath(msg.Content); path != "" && !isCommand(m.input.Value()) {
			cmd = m.loadImage(path)
		} else {
			cmd = m.pasteText(msg.Content)
		}
		return m, cmd
	case tea.KeyPressMsg:
		if m.loading != nil {
			m.loading.sendWhenReady = false
		}
		switch msg.String() {
		case "ctrl+v":
			cmd := m.loadImage("")
			return m, cmd
		case "ctrl+c", "ctrl+d":
			if m.input.Value() == "" && len(m.images) == 0 && m.loading == nil {
				return m, tea.Quit
			}
			if msg.String() == "ctrl+c" {
				m.input.Reset()
				m.images, m.loading = nil, nil
				return m, nil
			}
		case "enter":
			return m.submit()
		case "tab":
			if c, ok := m.completion(); ok {
				if len(c.options) > 0 {
					option := []rune(c.options[0])
					suffix := option[len([]rune(c.typed)):]
					m.input.InsertString(string(suffix) + " ")
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

func isCommand(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "//")
}

// submit sends the input box as a message, or runs it as a command.
func (m model) submit() (model, tea.Cmd) {
	text := m.input.Value()
	trimmed := strings.TrimSpace(text)
	if m.loading != nil {
		m.loading.sendWhenReady = true
		return m, nil
	}
	if trimmed == "" && len(m.images) == 0 {
		return m, nil
	}
	if !isCommand(trimmed) {
		if strings.HasPrefix(trimmed, "//") {
			text = strings.TrimPrefix(trimmed, "/")
		}
		if trimmed == "" {
			text = ""
		}
		if err := m.c.receive(message{author: human, text: text, images: m.images}); err != nil {
			m.c.note(err.Error())
		} else {
			m.remember(text)
			m.images = nil
			m.input.Reset()
		}
		return m, nil
	}
	text = trimmed
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
		m.remember(text)
		m.input.Reset()
		work := cmd.run(&m, arg)
		return m, work
	}
	m.c.note("no command /" + name + "; /help lists them")
	return m, nil
}

func (m *model) remember(text string) {
	if text != "" && (len(m.sent) == 0 || m.sent[len(m.sent)-1] != text) {
		m.sent = append(m.sent, text)
	}
	m.recall, m.draft = len(m.sent), ""
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
	if len(m.images) > 0 || m.loading != nil {
		var names []string
		for _, img := range m.images {
			names = append(names, "▧ "+img.name)
		}
		if m.loading != nil && m.loading.sendWhenReady {
			names = append(names, "sending when ready…")
		} else if m.loading != nil {
			names = append(names, "loading image…")
		}
		top = strings.Join(names, "  ") + "  " + top
	}
	top = ansi.Truncate(top, max(m.width-1, 1), "…")
	input := lipgloss.NewStyle().MaxWidth(m.width).Render(m.input.View())
	content := input
	if m.height > 1 {
		content = top + "\n" + input
	}
	v := tea.NewView(content)
	v.Cursor = m.input.Cursor()
	if v.Cursor != nil {
		if m.height > 1 {
			v.Cursor.Position.Y++
		}
		if v.Cursor.Position.X >= m.width {
			v.Cursor = nil
		}
	}
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
	if mention := mentionAtCursor.FindStringSubmatch(word); mention != nil {
		return complete(mention[1], m.c.memberNames(), nil), true
	}
	head := strings.Fields(string(line[:start]))
	switch {
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
		if strings.HasPrefix(strings.ToLower(s), strings.ToLower(typed)) {
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

func (c *chat) assignColor(pane string) {
	used := make([]int, len(palette))
	for p, a := range c.agents {
		if color, known := c.colors[p]; known && a.member && p != pane {
			used[color]++
		}
	}
	color, known := c.colors[pane]
	if !known || used[color] > 0 {
		color = slices.Index(used, slices.Min(used))
	}
	c.colors[pane] = color
}

func (c *chat) nameStyle(author, pane string) lipgloss.Style {
	if pane == "" && author == human {
		return bold.Foreground(lipgloss.Color("2"))
	}
	if color, ok := c.colors[pane]; ok {
		return bold.Foreground(lipgloss.Color(palette[color]))
	}
	return bold
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
		parts[i] = dot(a) + " " + c.nameStyle(a.Handle(), a.PaneID).Render(a.Handle())
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
