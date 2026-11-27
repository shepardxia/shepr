package chat

import (
	"bytes"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/yuin/goldmark/ast"
	ext "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Collect physical rows from prose that contains a pipe. Code, math and GFM
// tables are distinct nodes. Inline math can split a paragraph during parsing,
// so use the text segments that survive those transformations.
func looseRows(paragraph ast.Node, source []byte) []text.Segment {
	var rows []text.Segment
	for child := paragraph.FirstChild(); child != nil; child = child.NextSibling() {
		t, ok := child.(*ast.Text)
		if !ok || !bytes.ContainsRune(t.Value(source), '|') {
			continue
		}
		offset := t.Segment.Start
		if len(rows) > 0 && offset < rows[len(rows)-1].Stop {
			continue
		}
		start := bytes.LastIndexByte(source[:offset], '\n') + 1
		stop := len(source)
		if nl := bytes.IndexByte(source[offset:], '\n'); nl >= 0 {
			stop = offset + nl + 1
		}
		rows = append(rows, text.NewSegment(start, stop))
	}
	return rows
}

// Insert delimiter rows for groups of adjacent rows with matching columns.
// Goldmark can then handle alignment, escaped pipes and inline formatting.
func looseTables(root ast.Node, source []byte) []byte {
	var buf bytes.Buffer
	pos := 0
	for para := root.FirstChild(); para != nil; para = para.NextSibling() {
		if para.Kind() != ast.KindParagraph {
			continue
		}
		rows := looseRows(para, source)
		for i := 0; i+1 < len(rows); {
			first := rows[i]
			cols := pipeColumns(first.Value(source))
			start := i
			i++
			if cols < 2 || first.Start < pos {
				continue
			}
			for i < len(rows) && rows[i].Start == rows[i-1].Stop && pipeColumns(rows[i].Value(source)) == cols {
				i++
			}
			if i-start < 2 {
				continue
			}
			buf.Write(source[pos:first.Stop])
			buf.WriteString(strings.Repeat("|---", cols) + "|\n")
			last := rows[i-1].Stop
			buf.Write(source[first.Stop:last])
			if last < len(source) && source[last] != '\n' {
				buf.WriteByte('\n')
			}
			pos = last
		}
	}
	if buf.Len() == 0 {
		return nil
	}
	buf.Write(source[pos:])
	return buf.Bytes()
}

func pipeColumns(line []byte) int {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte("|")) && !bytes.Contains(line, []byte(" | ")) {
		return 0
	}
	line = bytes.TrimSuffix(bytes.TrimPrefix(line, []byte("|")), []byte("|"))
	n := 1
	for i, b := range line {
		if b == '|' && (i == 0 || line[i-1] != '\\') {
			n++
		}
	}
	return n
}

// Fit whole columns first, then share the remaining cells between long columns.
// In a narrow pane keep headers with their values by stacking each record.
func (r terminalMarkdown) table(t *ext.Table, width int) string {
	sizes := make([]int, len(t.Alignments))
	var rows [][]string
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		cells := make([]string, 0, len(sizes))
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			s := r.inlines(cell)
			i := len(cells)
			sizes[i] = max(sizes[i], lipgloss.Width(s), 1)
			cells = append(cells, s)
		}
		rows = append(rows, cells)
	}
	widths := columnWidths(sizes, width)
	if widths == nil {
		if len(rows) == 1 {
			return lipgloss.Wrap(strings.Join(rows[0], "  "), width, "")
		}
		var records []string
		for _, row := range rows[1:] {
			var fields []string
			for i, cell := range row {
				fields = append(fields, lipgloss.Wrap(bold.Render(rows[0][i])+": "+cell, width, ""))
			}
			records = append(records, strings.Join(fields, "\n"))
		}
		return strings.Join(records, "\n\n")
	}
	var lines []string
	for j, row := range rows {
		cells := make([]string, len(row))
		for i, cell := range row {
			style := lipgloss.NewStyle().Width(widths[i]).Bold(j == 0)
			switch t.Alignments[i] {
			case ext.AlignRight:
				style = style.Align(lipgloss.Right)
			case ext.AlignCenter:
				style = style.Align(lipgloss.Center)
			}
			if i+1 < len(row) {
				style = style.MarginRight(2)
			}
			cells[i] = style.Render(cell)
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, cells...))
		if j == 0 {
			var bars []string
			for _, w := range widths {
				bars = append(bars, strings.Repeat("─", w))
			}
			lines = append(lines, faint.Render(strings.Join(bars, "  ")))
		}
	}
	return strings.Join(lines, "\n")
}

// Columns narrower than five cells are useful for short values. When longer
// fields would also have to shrink that far, switch to stacked records.
func columnWidths(sizes []int, width int) []int {
	budget := width - 2*(len(sizes)-1)
	minWidth := 0
	for _, s := range sizes {
		minWidth += min(s, 5)
	}
	if budget < minWidth {
		return nil
	}
	widths := make([]int, len(sizes))
	for budget > 0 {
		before := budget
		for i, size := range sizes {
			if widths[i] < size && budget > 0 {
				widths[i]++
				budget--
			}
		}
		if budget == before {
			break
		}
	}
	return widths
}
