package chat

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

// A draft can have one pending paste. Its identity also rejects results
// arriving after Ctrl+C has cleared the draft.
type pendingPaste struct{ sendWhenReady bool }

type pasteResult struct {
	request *pendingPaste
	image   attachment
	paste   string
	err     error
}

func inputPath(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	if u, err := url.Parse(s); err == nil && u.Scheme == "file" && (u.Host == "" || u.Host == "localhost") {
		return u.Path
	}
	if strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			s = filepath.Join(home, s[2:])
		}
	}
	return strings.ReplaceAll(s, `\ `, " ")
}

func pastedImagePath(s string) string {
	if strings.ContainsAny(strings.TrimSpace(s), "\r\n") {
		return ""
	}
	path := inputPath(s)
	if filepath.IsAbs(path) {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
			return path
		}
	}
	return ""
}

// Keep reading and decoding off the UI loop. A canceled draft ignores its
// outstanding result, and Enter waits for the attachment to become ready.
func (m *model) loadImage(path string) tea.Cmd {
	if m.loading != nil {
		m.c.note("finish loading this image before attaching another")
		return nil
	}
	request := &pendingPaste{}
	m.loading = request
	dir, allowImagePath := m.c.dir, !isCommand(m.input.Value())
	return func() tea.Msg {
		result := pasteResult{request: request}
		file := path
		if file == "" {
			result.image, result.err = clipboardImage(dir)
			if !errors.Is(result.err, errNoClipboardImage) {
				return result
			}
			result.paste, result.err = clipboard.ReadAll()
			if result.err != nil || !allowImagePath {
				return result
			}
			file = pastedImagePath(result.paste)
			if file == "" {
				return result
			}
		}
		result.image, result.err = fileImage(dir, file)
		return result
	}
}

func (m *model) pasteText(content string) tea.Cmd {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(tea.PasteMsg{Content: plainText(content)})
	return cmd
}

func (m model) finishPaste(result pasteResult) (model, tea.Cmd) {
	if result.request != m.loading {
		return m, nil
	}
	m.loading = nil
	if result.err != nil {
		m.c.note(result.err.Error())
		return m, nil
	}
	var cmd tea.Cmd
	if result.image.path != "" {
		m.images = append(m.images, result.image)
	} else {
		cmd = m.pasteText(result.paste)
	}
	if result.request.sendWhenReady {
		next, send := m.submit()
		return next, tea.Batch(cmd, send)
	}
	return m, cmd
}

func (m *model) attach(arg string) tea.Cmd { return m.loadImage(inputPath(arg)) }

func (m *model) detach(_ string) tea.Cmd {
	if len(m.images) > 0 {
		m.images = m.images[:len(m.images)-1]
	}
	return nil
}

func (m *model) add(arg string) tea.Cmd {
	if m.pending != "" {
		m.c.note("an agent is already starting")
		return nil
	}
	cmd := m.c.add(arg)
	if cmd != nil {
		m.pending = "/add " + arg + " …"
	}
	return cmd
}
