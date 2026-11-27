package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	pass "github.com/gohugoio/hugo-goldmark-extensions/passthrough"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	ext "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var markdown = goldmark.New(goldmark.WithExtensions(extension.Table, roomMath{}))
var bold = lipgloss.NewStyle().Bold(true)

const mentionStart = `(?i)(?:^|[\s,;:!?("'\[\{])@`
const handle = `[a-z][a-z0-9_-]*`

var mentionRe = regexp.MustCompile(mentionStart + "(" + handle + ")")
var mentionAtCursor = regexp.MustCompile(mentionStart + "(" + handle + "|)$")

// plainText strips terminal instructions at ingress while preserving indentation.
func plainText(s string) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(ansi.Strip(s))
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}

type markdownBody struct {
	source []byte
	root   ast.Node
}

func parseBody(s string) markdownBody {
	source := []byte(strings.ReplaceAll(s, "\t", "    "))
	if t := bytes.TrimSpace(source); len(t) != 0 && (t[0] == '{' || t[0] == '[') {
		if formatted, ok := prettyJSON(t); ok {
			return markdownBody{source: formatted}
		}
	}
	root := markdown.Parser().Parse(text.NewReader(source))
	if bytes.ContainsRune(source, '|') {
		if normalized := looseTables(root, source); normalized != nil {
			source = normalized
			root = markdown.Parser().Parse(text.NewReader(source))
		}
	}
	return markdownBody{source, root}
}

func (b markdownBody) mentions() []string {
	if b.root == nil {
		return nil
	}
	var names []string
	_ = ast.Walk(b.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch v := n.(type) {
			case *ast.CodeSpan:
				return ast.WalkSkipChildren, nil
			case *ast.Text:
				for _, m := range mentionRe.FindAllSubmatch(v.Value(b.source), -1) {
					names = append(names, string(m[1]))
				}
			}
		}
		return ast.WalkContinue, nil
	})
	return names
}

type terminalMarkdown struct {
	source []byte
	names  map[string]lipgloss.Style
}

func (r terminalMarkdown) inlines(n ast.Node) string {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch v := child.(type) {
		case *pass.PassthroughInline:
			b.WriteString(renderMath(withoutDelimiters(v.Segment.Value(r.source), v.Delimiters), true, 0))
		case *ast.Text:
			value := mentionRe.ReplaceAllStringFunc(string(v.Value(r.source)), func(s string) string {
				lead, name, _ := strings.Cut(s, "@")
				if style, ok := r.names[strings.ToLower(name)]; ok {
					return lead + style.Render("@"+name)
				}
				return s
			})
			b.Write(util.UnescapePunctuations([]byte(value)))
			if v.SoftLineBreak() || v.HardLineBreak() {
				b.WriteByte('\n')
			}
		case *ast.CodeSpan:
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(string(v.Text(r.source))))
		case *ast.Emphasis:
			b.WriteString(lipgloss.NewStyle().Italic(v.Level == 1).Bold(v.Level == 2).Render(r.inlines(v)))
		case *ast.Link:
			b.WriteString(r.inlines(v) + " (" + string(v.Destination) + ")")
		case *ast.Image:
			b.WriteString(r.inlines(v) + " (" + string(v.Destination) + ")")
		case *ast.AutoLink:
			b.Write(v.Label(r.source))
		case *ast.RawHTML:
			b.Write(v.Segments.Value(r.source))
		case *ast.String:
			b.Write(v.Value)
		default:
			b.WriteString(r.inlines(child))
		}
	}
	return b.String()
}

func (r terminalMarkdown) blocks(n ast.Node, width int) string {
	var b strings.Builder
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if child != n.FirstChild() {
			b.WriteByte('\n')
			if child.HasBlankPreviousLines() {
				b.WriteByte('\n')
			}
		}
		b.WriteString(r.block(child, width))
	}
	return b.String()
}

func (r terminalMarkdown) block(n ast.Node, width int) string {
	switch v := n.(type) {
	case *pass.PassthroughBlock:
		return renderMath(withoutDelimiters(v.Lines().Value(r.source), v.Delimiters), false, width)
	case *ast.Paragraph, *ast.TextBlock:
		return lipgloss.Wrap(r.inlines(n), width, "")
	case *ast.Heading:
		return lipgloss.Wrap(bold.Render(r.inlines(n)), width, "")
	case *ast.ThematicBreak:
		return faint.Render(strings.Repeat("─", min(width, 32)))
	case *ast.FencedCodeBlock:
		code := v.Lines().Value(r.source)
		switch strings.ToLower(string(v.Language(r.source))) {
		case "math":
			return renderMath(code, false, width)
		case "json":
			code, _ = prettyJSON(code)
		}
		return literal(code, width)
	case *ast.CodeBlock, *ast.HTMLBlock:
		return literal(n.Text(r.source), width)
	case *ast.Blockquote:
		return r.prefixed(n, "│ ", "│ ", width)
	case *ast.List:
		var items []string
		i := v.Start
		for item := v.FirstChild(); item != nil; item = item.NextSibling() {
			prefix := "• "
			if v.IsOrdered() {
				prefix = fmt.Sprintf("%d%c ", i, v.Marker)
				i++
			}
			items = append(items, r.prefixed(item, prefix, strings.Repeat(" ", ansi.StringWidth(prefix)), width))
		}
		return strings.Join(items, "\n")
	case *ext.Table:
		return r.table(v, width)
	}
	return r.blocks(n, width)
}

func (r terminalMarkdown) prefixed(n ast.Node, first, rest string, width int) string {
	w := ansi.StringWidth(first)
	if w+2 > width {
		return r.blocks(n, width)
	}
	return first + strings.ReplaceAll(r.blocks(n, width-w), "\n", "\n"+rest)
}

func prettyJSON(source []byte) ([]byte, bool) {
	var buf bytes.Buffer
	if json.Indent(&buf, bytes.TrimSpace(source), "", "  ") == nil {
		return buf.Bytes(), true
	}
	return source, false
}

func literal(source []byte, width int) string {
	return ansi.Hardwrap(strings.TrimSuffix(string(source), "\n"), width, true)
}

func (c *chat) render(msg message, width int) string {
	if width <= 1 {
		return singleColumn(c.render(msg, 2))
	}
	if msg.audience == noteAudience {
		indent := strings.Repeat(" ", min(2, width-2))
		return faint.Render(indent + strings.ReplaceAll(lipgloss.Wrap(strings.ReplaceAll(msg.text, "\t", "    "), width-len(indent), ""), "\n", "\n"+indent))
	}
	head := c.nameStyle(msg.author, msg.pane).Render(msg.author) + "  " + faint.Render(msg.at.Format("15:04"))
	parts := []string{"", lipgloss.Wrap(head, width, "")}
	if msg.text != "" {
		r := terminalMarkdown{source: msg.body.source, names: map[string]lipgloss.Style{}}
		for _, a := range c.members() {
			r.names[strings.ToLower(a.Handle())] = c.nameStyle(a.Handle(), a.PaneID)
		}
		if msg.body.root == nil {
			parts = append(parts, literal(r.source, width))
		} else {
			block := r.blocks(msg.body.root, width)
			if block == "" {
				block = literal(r.source, width)
			}
			parts = append(parts, block)
		}
	}
	for _, img := range msg.images {
		parts = append(parts, img.render(width))
	}
	return strings.Join(parts, "\n")
}

// Fall back to a replacement glyph for an indivisible two-cell grapheme.
func singleColumn(block string) string {
	var narrow strings.Builder
	for rest := ansi.Strip(block); len(rest) > 0; {
		cluster, w := ansi.FirstGraphemeCluster(rest, ansi.GraphemeWidth)
		if w > 1 {
			narrow.WriteRune('�')
		} else {
			narrow.WriteString(cluster)
		}
		rest = rest[len(cluster):]
	}
	return ansi.Hardwrap(narrow.String(), 1, true)
}
