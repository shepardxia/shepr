package chat

import (
	"github.com/shepardxia/shepr/internal/herdr"

	"fmt"
	"regexp"
	"strings"
)

// welcomeText is an agent's first message from the chat, sent on its own. Its
// arguments are the human, the chat pane, the name line, the chat pane again,
// and the name to post under.
const welcomeText = `Welcome to the group chat! %s opened it for the agents in this tab, in the shepr chat pane (%s).

%s

Messages reach you as "name: message", and you post the same way:

herdr pane send-text %s "%s: <message>"

The others never see this terminal, so answer in the chat and don't repeat yourself here. You don't have to answer everything; post when you have something to add.

House rule: keep it short and to the point, like texting a colleague. No preamble, no recaps.`

func welcomeFor(name string, named bool, chatPane string) string {
	line, poster := "You go by "+name+" here.", name
	if !named {
		line = "Pick a name for yourself first, something creative and random, one lowercase word: herdr agent rename " + name + " <name>"
		poster = "<name>"
	}
	return fmt.Sprintf(welcomeText, human, chatPane, line, chatPane, poster)
}

// welcome greets an agent that joined the chat. An unnamed agent first gets a
// placeholder herdr name, so it can rename itself by name rather than pane id.
func welcome(a herdr.Agent, all []herdr.Agent, chatPane string) error {
	name := a.Name
	if name == "" {
		taken := map[string]bool{}
		for _, o := range all {
			taken[o.Name] = true
		}
		name = freeName(a.Kind, taken)
		if err := herdr.Rename(a.PaneID, name); err != nil {
			return err
		}
	}
	return herdr.Prompt(a.PaneID, welcomeFor(name, a.Name != "", chatPane))
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
