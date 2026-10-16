package chat

import (
	"io"
	"regexp"
	"strings"
	"time"
)

// editKeys rewrites the modified arrows herdr sends into keys the input line
// knows: Option or Ctrl with an arrow jumps a word, Cmd with an arrow goes to
// the start or end of the line.
var editKeys = strings.NewReplacer(
	"\x1b[1;3D", "\x1bb", "\x1b[1;3C", "\x1bf",
	"\x1b[1;5D", "\x1bb", "\x1b[1;5C", "\x1bf",
	"\x1b[1;9D", "\x01", "\x1b[1;9C", "\x05",
)

// The chat turns on bracketed paste, so a paste arrives between these markers.
const pasteStart, pasteEnd = "\x1b[200~", "\x1b[201~"

// pastedBreak stands in for a line break in pasted text, since the input line
// ends at a real one; the chat turns it back into a line break on send.
const pastedBreak = "⏎"

// pastedText flattens pasted text for the input line, which drops tabs.
var pastedText = strings.NewReplacer("\r\n", pastedBreak, "\r", pastedBreak, "\n", pastedBreak, "\t", "    ")

// unpaste replaces each bracketed paste in s with its text on one line.
func unpaste(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, pasteStart)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		s = s[i+len(pasteStart):]
		end := strings.Index(s, pasteEnd)
		if end < 0 {
			end = len(s)
		}
		b.WriteString(pastedText.Replace(strings.TrimRight(s[:end], "\r\n")))
		s = s[min(end+len(pasteEnd), len(s)):]
	}
}

// pasteOpen reports whether s ends inside a paste.
func pasteOpen(s string) bool {
	i := strings.LastIndex(s, pasteStart)
	return i >= 0 && !strings.Contains(s[i:], pasteEnd)
}

// burstGap is how long input may pause and still count as one burst. herdr
// pane send-text writes a whole message at once; typing is far slower.
const burstGap = 25 * time.Millisecond

var sentRe = regexp.MustCompile(`^([a-z][a-z0-9_-]{0,31}):\s*([\s\S]*\S)\s*$`)

// sent reads a burst as a message an agent sent with herdr pane send-text: one
// write that starts with a member's name and a colon. member resolves a name to
// the agent's pane.
func sent(burst string, member func(string) (string, bool)) (author, pane, text string, ok bool) {
	m := sentRe.FindStringSubmatch(burst)
	if m == nil {
		return "", "", "", false
	}
	if pane, ok = member(m[1]); !ok {
		return "", "", "", false
	}
	return m[1], pane, m[2], true
}

// splitInput reads the terminal, hands agent messages to fromAgent, and passes
// everything else, keystrokes and the human's own pastes flattened to one
// line, to the returned reader for the input line.
func splitInput(in io.Reader, member func(string) (string, bool), fromAgent func(author, pane, text string)) io.ReadCloser {
	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for burst := range chunks {
			for more := true; more; {
				select {
				case next, ok := <-chunks:
					if !ok {
						more = false
					} else {
						burst = append(burst, next...)
					}
				case <-time.After(burstGap):
					more = false
				}
			}
			for pasteOpen(string(burst)) {
				next, ok := <-chunks
				if !ok {
					break
				}
				burst = append(burst, next...)
			}
			in := string(burst)
			if !strings.Contains(in, pasteStart) {
				if author, pane, text, ok := sent(in, member); ok {
					fromAgent(author, pane, text)
					continue
				}
			}
			if _, err := pw.Write([]byte(editKeys.Replace(unpaste(in)))); err != nil {
				return
			}
		}
	}()
	return pr
}
