package chat

import (
	"fmt"
	"regexp"
	"strings"
)

const welcomeText = `You're in %s's group chat (%s).

First, pick a name, one lowercase word under 10 letters:
herdr agent rename %s <name>

Post with: shepr post <name> "<message>"

An @name exchange stays in chat until %s addresses the whole room again.

Your terminal's answers aren't shared. Post when you have something to add, and keep it short.`

// leaveText tells an agent it was removed from the room. Its arguments are
// the human and the chat pane.
const leaveText = `%s removed you from the group chat in the shepr chat pane (%s). Stop posting there; its messages no longer reach you, and you'll be welcomed back if you're added again.`

// welcomeFor addresses the agent by pane so it can choose its own name.
func welcomeFor(agentPane, chatPane string, history []message) string {
	text := fmt.Sprintf(welcomeText, human, chatPane, agentPane, human)
	if len(history) == 0 {
		return text
	}
	parts := make([]string, len(history))
	for i, m := range history {
		parts[i] = m.String()
	}
	return text + "\n\nHere's what you missed, latest last:\n\n" + strings.Join(parts, "\n\n")
}

var nameJunk = regexp.MustCompile(`[^a-z0-9_-]+`)

// freeName is a name derived from kind that no live agent holds. herdr names
// are global and match [a-z][a-z0-9_-]{0,31}.
func freeName(kind string, taken map[string]bool) string {
	base := strings.Trim(nameJunk.ReplaceAllString(strings.ToLower(kind), "-"), "-")
	if base == "" || base[0] < 'a' || base[0] > 'z' {
		base = strings.TrimSuffix("agent-"+base, "-")
	}
	if len(base) > 28 {
		base = base[:28]
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s%d", base, i)
	}
	return name
}
