package chat

import (
	"io"
	"regexp"
	"strings"
	"time"
)

// burstGap is how long input may pause and still count as one burst. herdr
// pane send-text writes a whole message at once; typing is far slower.
const burstGap = 25 * time.Millisecond

// A paste arrives between these markers.
const pasteStart, pasteEnd = "\x1b[200~", "\x1b[201~"

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
// everything else, keystrokes and the human's pastes, to the returned reader.
func splitInput(in io.Reader, member func(string) (string, bool), fromAgent func(author, pane, text string)) io.Reader {
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
			// A paste is the human's text, however it starts, so it is kept
			// whole until it ends.
			for s := string(burst); strings.LastIndex(s, pasteStart) > strings.LastIndex(s, pasteEnd); s = string(burst) {
				next, ok := <-chunks
				if !ok {
					break
				}
				burst = append(burst, next...)
			}
			if !strings.Contains(string(burst), pasteStart) {
				if author, pane, text, ok := sent(string(burst), member); ok {
					fromAgent(author, pane, text)
					continue
				}
			}
			if _, err := pw.Write(burst); err != nil {
				return
			}
		}
	}()
	return pr
}
