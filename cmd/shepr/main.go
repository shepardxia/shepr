// Command shepr is a herdr plugin: a group chat for the agents in a tab.
package main

import (
	"fmt"
	"os"

	"github.com/shepardxia/shepr/internal/chat"
	"github.com/shepardxia/shepr/internal/open"
)

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "chat":
		err = chat.Run()
	case "open":
		err = open.Run()
	default:
		fmt.Fprintln(os.Stderr, "usage: shepr chat|open")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "shepr:", err)
		os.Exit(1)
	}
}
