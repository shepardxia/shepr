package chat

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Posts travel separately from terminal input, one JSON message per connection.
type post struct {
	Pane  string `json:"pane"`
	Text  string `json:"text"`
	Local bool   `json:"local"`
}

type postMsg struct {
	post  post
	reply chan string
}

// Post sends an agent's message to a running chat. --local displays a reply
// without prompting the room. The caller's herdr pane identifies the author.
func Post(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: shepr post <socket> [--local] <message>")
	}
	addr, args := args[0], args[1:]
	local := args[0] == "--local"
	if local {
		args = args[1:]
	}
	p := post{Pane: os.Getenv("HERDR_PANE_ID"), Text: strings.TrimSpace(strings.Join(args, " ")), Local: local}
	if p.Pane == "" || p.Text == "" {
		return errors.New("post needs a message and an agent's HERDR_PANE_ID")
	}
	conn, err := net.DialTimeout("unix", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(p); err != nil {
		return err
	}
	var problem string
	if err := json.NewDecoder(conn).Decode(&problem); err != nil {
		return err
	}
	if problem != "" {
		return errors.New(problem)
	}
	return nil
}

func listenPosts(send func(tea.Msg), done <-chan struct{}) (string, func(), error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "shepr-")
	if err != nil {
		return "", nil, err
	}
	addr := filepath.Join(dir, "chat.sock")
	listener, err := net.Listen("unix", addr)
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	command := quote(exe) + " post " + quote(addr)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go readPost(conn, send, done)
		}
	}()
	return command, func() { listener.Close(); os.RemoveAll(dir) }, nil
}

func readPost(conn net.Conn, send func(tea.Msg), done <-chan struct{}) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var p post
	problem := ""
	if err := json.NewDecoder(conn).Decode(&p); err != nil {
		problem = err.Error()
	} else {
		reply := make(chan string, 1)
		send(postMsg{p, reply})
		select {
		case problem = <-reply:
		case <-done:
			return
		case <-time.After(5 * time.Second):
			problem = "chat did not acknowledge the post"
		}
	}
	_ = json.NewEncoder(conn).Encode(problem)
}
