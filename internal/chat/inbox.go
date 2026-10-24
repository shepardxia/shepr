package chat

import (
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// burstGap is how long the rest of a post may trail its first read: herdr pane
// send-text writes a post at once, but the terminal hands a long one to the
// pane in several reads.
const burstGap = 25 * time.Millisecond

// A paste arrives between these markers.
const pasteStart, pasteEnd = "\x1b[200~", "\x1b[201~"

// postRe matches a read that starts a post sent with herdr pane send-text: a
// name and a colon.
var postRe = regexp.MustCompile(`^([a-z][a-z0-9_-]{0,31}):`)

// isKey reports whether a read is a single keystroke: one character or one
// escape sequence.
func isKey(b []byte) bool { return b[0] == 0x1b || utf8.RuneCount(b) == 1 }

// splitInput reads the terminal, hands posts to fromAgent, and passes
// everything else, keystrokes and the human's pastes, to the returned reader.
// member resolves a post's name to its author's pane, empty for a name it does
// not know; such a post still goes to fromAgent.
func splitInput(in io.Reader, member func(string) string, fromAgent func(author, pane, text string)) io.Reader {
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
		pasting := false
		for chunk := range chunks {
			for chunk != nil {
				m := postRe.FindSubmatch(chunk)
				if m == nil || pasting {
					// A paste is the human's text, however its reads start.
					if s := string(chunk); strings.Contains(s, pasteStart) || strings.Contains(s, pasteEnd) {
						pasting = strings.LastIndex(s, pasteStart) > strings.LastIndex(s, pasteEnd)
					}
					if _, err := pw.Write(chunk); err != nil {
						return
					}
					break
				}
				var post []byte
				post, chunk = gather(chunk, chunks)
				if text := strings.TrimSpace(string(post[len(m[0]):])); text != "" {
					fromAgent(string(m[1]), member(string(m[1])), text)
				}
			}
		}
	}()
	return pr
}

// gather joins the post that starts with first and the reads within burstGap
// that carry the rest of it. A keystroke or another post ends it and is
// returned as after.
func gather(first []byte, chunks <-chan []byte) (post, after []byte) {
	post = first
	for {
		select {
		case next, ok := <-chunks:
			if !ok {
				return post, nil
			}
			if isKey(next) || postRe.Match(next) {
				return post, next
			}
			post = append(post, next...)
		case <-time.After(burstGap):
			return post, nil
		}
	}
}
