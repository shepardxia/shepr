package chat

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Posts travel separately from terminal input, one JSON message per connection.
type post struct {
	Pane  string `json:"pane"`
	Text  string `json:"text"`
	Local bool   `json:"local"`
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

func (c *chat) listenPosts() (func(), error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "shepr-")
	if err != nil {
		return nil, err
	}
	addr := filepath.Join(dir, "chat.sock")
	listener, err := net.Listen("unix", addr)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	c.post = quote(exe) + " post " + quote(addr)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go c.readPost(conn)
		}
	}()
	return func() { listener.Close(); os.RemoveAll(dir) }, nil
}

func (c *chat) readPost(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var p post
	problem := ""
	if err := json.NewDecoder(conn).Decode(&p); err != nil {
		problem = err.Error()
	} else {
		c.mu.Lock()
		_, in := c.room[p.Pane]
		c.mu.Unlock()
		if !in {
			problem = "your agent is not in this room"
		} else {
			c.p.Send(message{pane: p.Pane, text: p.Text, local: p.Local})
		}
	}
	_ = json.NewEncoder(conn).Encode(problem)
}
