package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const resultsTable = "Run | hit | log-score | solved in 16 | AUROC with Other | named only\nv35_mech | .362 | −4.75 | .796 | .504 | .697\nv35 | .371 | −4.77 | .821 | .516 | .708\nv35_jev | .381 | −4.66 | .818 | .668 | .706"

func rendered(t *testing.T, s string, width int) string {
	t.Helper()
	c := newRoom()
	if err := c.receive(message{author: "tester", text: s}); err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(ansi.Strip(c.render(c.transcript[0], width)), "\n", 3)
	return lines[2]
}

func TestTableLayout(t *testing.T) {
	for _, s := range []string{
		resultsTable,
		strings.Replace(resultsTable, "\nv35_mech", "\n:---|---:|---:|---:|---:|---:\nv35_mech", 1),
	} {
		for _, width := range []int{140, 74, 54, 35, 17} {
			got := rendered(t, "At steps 46–74, same cases per step:\n\n"+s, width)
			lines := strings.Split(got, "\n")
			for _, line := range lines {
				if ansi.StringWidth(line) > width {
					t.Fatalf("overflows at %d: %q", width, line)
				}
			}
			for _, value := range []string{".362", "−4.77", ".818", ".668", ".706"} {
				if !strings.Contains(got, value) {
					t.Fatalf("lost %q at %d:\n%s", value, width, got)
				}
			}
			if width == 74 {
				var rows []string
				for _, line := range lines {
					if strings.HasPrefix(line, "v35") {
						rows = append(rows, line)
					}
				}
				if len(rows) != 3 {
					t.Fatalf("rows at %d:\n%s", width, got)
				}
				for i := range 6 {
					var col int
					for j, row := range rows {
						fields := strings.Fields(row)
						if len(fields) != 6 {
							t.Fatalf("row %q", row)
						}
						x := ansi.StringWidth(row[:strings.Index(row, fields[i])])
						if j != 0 && col != x {
							t.Fatalf("misaligned column %d:\n%s", i, got)
						}
						col = x
					}
				}
			}
			if width == 35 && !strings.Contains(got, "AUROC with Other: .668") {
				t.Fatalf("expected stacked fields:\n%s", got)
			}
		}
	}
}

func TestStructuredBodies(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"Before\nCol | Other\na | b\nAfter", "After"},
		{"```sh\na | b\nc | d\n  echo hi\n```", "a | b\nc | d\n  echo hi"},
		{"    indent\n        more", "indent\n    more"},
		{"{\"n\":9007199254740993,\"x\":[1,2]}", "  \"n\": 9007199254740993,"},
		{"```json\n{\"name\":\"@example\"}\n```", "  \"name\": \"@example\""},
		{"```json\n{broken\n```", "{broken"},
		{"| item | value |\n|---|---:|\n| 猫 | a\\|b |", "a|b"},
		{"# Review\n\n- **first**\n- second", "• first\n• second"},
		{"\x1b[2Jsafe\x1b[31m color\x1b[0m", "safe color"},
	} {
		if got := rendered(t, tc.input, 60); !strings.Contains(got, tc.want) {
			t.Errorf("%q missing %q in %q", tc.input, tc.want, got)
		}
	}
}

func TestMathInLooseTable(t *testing.T) {
	source := "Model | Score\nA | $x^2$\nB | \\(y^3\\)\nAfter"
	got := rendered(t, source, 40)
	if strings.Contains(got, "|") || !strings.Contains(got, "─") || !strings.Contains(got, "x²") || !strings.Contains(got, "y³") || !strings.HasSuffix(got, "After") {
		t.Fatalf("lost table, math or following prose:\n%s", got)
	}
}

func TestNarrowPaneResize(t *testing.T) {
	c := newRoom()
	s := "**測試** 👩🏽‍💻\n\n| run | Δ |\n|---|---|\n| v35 | 界 |\n\n```\n  長い longwordlongwordlongword\n```"
	if err := c.receive(message{author: "猫", text: s}); err != nil {
		t.Fatal(err)
	}
	m := newModel(c)
	m.input.SetValue(strings.Repeat("長い👩🏽‍💻 hello\n", 12))
	for _, width := range []int{74, 35, 9, 4, 3, 2, 1, 60} {
		for _, height := range []int{7, 2, 1} {
			m, _ = m.update(tea.WindowSizeMsg{Width: width, Height: height})
			if h := strings.Count(m.View().Content, "\n") + 1; h > height {
				t.Errorf("input height %d at %dx%d", h, width, height)
			}
			for _, view := range []string{m.View().Content, c.render(c.transcript[0], width)} {
				for _, line := range strings.Split(ansi.Strip(view), "\n") {
					if w := ansi.StringWidth(line); w > width {
						t.Fatalf("width %d at %dx%d: %q", w, width, height, line)
					}
				}
			}
		}
	}
}
