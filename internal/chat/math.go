package chat

import (
	"bytes"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/doug/termtex"
	pass "github.com/gohugoio/hugo-goldmark-extensions/passthrough"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Passthrough handles multiline display math and TeX delimiters in Markdown containers.
// Single-dollar inline math also needs currency-aware opening and closing rules.
type roomMath struct{}

func (roomMath) Extend(md goldmark.Markdown) {
	pass.New(pass.Config{
		InlineDelimiters: []pass.Delimiters{{Open: `\(`, Close: `\)`}},
		BlockDelimiters:  []pass.Delimiters{{Open: "$$", Close: "$$"}, {Open: `\[`, Close: `\]`}},
	}).Extend(md)
	md.Parser().AddOptions(parser.WithInlineParsers(util.Prioritized(dollarParser{}, 500)))
}

type dollarParser struct{}

func (dollarParser) Trigger() []byte { return []byte{'$'} }
func (dollarParser) Parse(_ ast.Node, reader text.Reader, _ parser.Context) ast.Node {
	line, segment := reader.PeekLine()
	if len(line) < 3 || line[1] == '$' || util.IsSpace(line[1]) {
		return nil
	}
	for i := 2; i < len(line); i++ {
		if line[i] == '\\' {
			i++
			continue
		}
		if line[i] != '$' {
			continue
		}
		if util.IsSpace(line[i-1]) || (i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9') {
			return nil
		}
		reader.Advance(i + 1)
		return &pass.PassthroughInline{
			Segment:    segment.WithStop(segment.Start + i + 1),
			Delimiters: &pass.Delimiters{Open: "$", Close: "$"},
		}
	}
	return nil
}

func withoutDelimiters(source []byte, delimiters *pass.Delimiters) []byte {
	source = bytes.TrimSpace(source)
	return bytes.TrimSuffix(bytes.TrimPrefix(source, []byte(delimiters.Open)), []byte(delimiters.Close))
}

// Math is typeset for the room pane. Delivery and agent recaps use the authored TeX.
func renderMath(expression []byte, inline bool, width int) string {
	source := string(expression)
	rendered, err := termtex.Render(source, termtex.Style{Inline: inline, Width: width})
	if err != nil {
		delimiter := "$"
		if !inline {
			delimiter = "$$"
		}
		rendered = delimiter + source + delimiter
	}
	if width > 0 {
		// An indivisible expression (for example a wide matrix) can exceed the typesetter's width.
		return ansi.Hardwrap(rendered, width, true)
	}
	return strings.TrimRight(rendered, " ")
}
