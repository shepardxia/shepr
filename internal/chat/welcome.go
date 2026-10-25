package chat

import (
	"fmt"
	"regexp"
	"strings"
)

// welcomeText is an agent's first message from the chat. Its arguments are the
// human, the chat pane, the agent's herdr name, and the chat pane again.
const welcomeText = `Hey, welcome to the group chat! %s set it up in the shepr chat pane (%s).

First, pick a name, one lowercase word under 10 letters:
herdr agent rename %s <name>

Messages show up as "name: message". To post:
herdr pane send-text %s "<name>: <message>"

The others never see this terminal, so answer in the chat and don't repeat yourself here. You don't have to answer everything; post when you have something to add.

House rule: keep it short and to the point, like texting a reliable colleague.`

// leaveText tells an agent it was removed from the room. Its arguments are
// the human and the chat pane.
const leaveText = `%s removed you from the group chat in the shepr chat pane (%s). Stop posting there; its messages no longer reach you, and you'll be welcomed back if you're added again.`

// welcomeFor is the welcome for the agent named name, followed by a recap of
// history, the latest messages of the chat.
func welcomeFor(name, chatPane string, history []message) string {
	text := fmt.Sprintf(welcomeText, human, chatPane, name, chatPane)
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
