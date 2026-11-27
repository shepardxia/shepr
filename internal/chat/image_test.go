package chat

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func testPNG(t *testing.T) (string, []byte) {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 6, 4))
	im.Set(0, 0, color.RGBA{155, 22, 10, 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, im); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "my chart.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path, encoded.Bytes()
}

func TestComposeImages(t *testing.T) {
	path, data := testPNG(t)
	c := newRoom()
	c.dir = t.TempDir()
	m := newModel(c)
	m, _ = m.update(tea.WindowSizeMsg{Width: 54, Height: 6})
	m.input.SetValue("/attach ")
	m, _ = m.update(tea.PasteMsg{Content: path})
	if m.input.Value() != "/attach "+path {
		t.Fatal("pasted path was taken away from /attach")
	}
	m, cmd := m.submit()
	if cmd == nil || m.loading == nil {
		t.Fatal("attachment did not start")
	}
	m.input.SetValue("@rook,@missing review")
	m, _ = m.submit()
	if len(c.transcript) != 0 {
		t.Fatal("submitted while loading")
	}
	m, _ = m.update(cmd())
	if len(m.images) != 1 || !strings.Contains(ansi.Strip(m.View().Content), "my chart.png") {
		t.Fatal("missing staged image")
	}
	stored := m.images[0].path
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	m, _ = m.submit()
	if len(m.images) != 1 || m.input.Value() != "@rook,@missing review" {
		t.Fatal("rejection lost draft")
	}
	m.input.SetValue("@rook,@pauley review")
	m, _ = m.submit()
	msg := c.transcript[len(c.transcript)-1]
	if m.input.Value() != "" || len(m.images) != 0 || len(msg.images) != 1 {
		t.Fatal("failed to send attachment")
	}
	for _, a := range c.agents {
		if len(a.pending) != 1 || !strings.Contains(a.pending[0], stored) || !strings.Contains(a.pending[0], msg.text) {
			t.Fatalf("prompt missing original: %v", a.pending)
		}
	}
	if original, err := os.ReadFile(stored); err != nil || !bytes.Equal(original, data) {
		t.Fatal("original was not retained")
	}
	for _, width := range []int{3, 11, 40} {
		view := ansi.Strip(c.render(msg, width))
		if !strings.Contains(view, "▀") {
			t.Fatal("missing thumbnail")
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("image exceeds %d: %q", width, line)
			}
		}
	}
	m, cmd = m.update(tea.PasteMsg{Content: "'" + stored + "'"})
	m, _ = m.submit()
	m, _ = m.update(cmd())
	if len(c.recap()) != 1 || !strings.Contains(c.recap()[0].String(), stored) {
		t.Fatal("image-only broadcast missing from recap")
	}
}

func TestImagePostSocket(t *testing.T) {
	path, _ := testPNG(t)
	c := newRoom()
	dir := t.TempDir()
	m := newModel(c)
	exchange := func(p post) postReply {
		t.Helper()
		server, client := net.Pipe()
		done := make(chan struct{})
		defer close(done)
		defer client.Close()
		go readPost(dir, server, func(msg tea.Msg) { m.update(msg) }, done)
		if err := json.NewEncoder(client).Encode(p); err != nil {
			t.Fatal(err)
		}
		var reply postReply
		if err := json.NewDecoder(client).Decode(&reply); err != nil {
			t.Fatal(err)
		}
		return reply
	}
	if r := exchange(post{Pane: "w1:p2"}); !r.Member || !r.SupportsImages {
		t.Fatal(r)
	}
	p, err := parsePost([]string{"rook", "--image", path})
	if err != nil {
		t.Fatal(err)
	}
	p.Pane = "w1:p2"
	if r := exchange(p); r.Error != "" {
		t.Fatal(r.Error)
	}
	if len(c.transcript) != 1 || len(c.transcript[0].images) != 1 || len(c.agents["w1:p3"].pending) != 1 {
		t.Fatal("image-only post handled like a membership probe")
	}
	p.Images = append(p.Images, filepath.Join(dir, "missing"))
	if r := exchange(p); r.Error == "" || len(c.transcript) != 1 {
		t.Fatal("invalid image batch was partially posted")
	}
	p, err = parsePost([]string{"rook", "--image", path, "--image", path, "--", "--image", "as text"})
	if err != nil || len(p.Images) != 2 || p.Text != "--image as text" {
		t.Fatal(p, err)
	}
}

func TestCanceledPasteCannotSendNextDraft(t *testing.T) {
	path, _ := testPNG(t)
	c := newRoom()
	c.dir = t.TempDir()
	m := newModel(c)
	m, first := m.update(tea.PasteMsg{Content: path})
	m, _ = m.submit()
	m, _ = m.update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m, next := m.update(tea.PasteMsg{Content: path})
	m, _ = m.update(first())
	if m.loading == nil || len(m.images) != 0 || len(c.transcript) != 0 {
		t.Fatal("a canceled paste changed the next draft")
	}
	m, _ = m.update(next())
	if m.loading != nil || len(m.images) != 1 || len(c.transcript) != 0 {
		t.Fatal("the next paste inherited the canceled send")
	}
}
