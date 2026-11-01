package chat

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
)

// An empty Text queries membership; otherwise the request posts a message.
type post struct {
	Pane string `json:"pane"`
	Text string `json:"text,omitempty"`
}

type postReply struct {
	Member bool   `json:"member"`
	Error  string `json:"error,omitempty"`
}

type postMsg struct {
	post  post
	reply chan postReply
}

func roomsDir() (string, error) {
	root, err := os.UserCacheDir()
	return filepath.Join(root, "shepr"), err
}

// Post finds the caller's room before sending the message to it once.
func Post(args []string) error {
	p := post{Pane: os.Getenv("HERDR_PANE_ID"), Text: strings.TrimSpace(strings.Join(args, " "))}
	if p.Pane == "" || p.Text == "" {
		return errors.New("usage: shepr post <message> (from an agent's herdr pane)")
	}
	root, err := roomsDir()
	if err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(root, "chat-*", "chat.sock"))
	if err != nil {
		return err
	}
	room := ""
	for _, path := range paths {
		reply, err := exchangePost(path, post{Pane: p.Pane})
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			continue // a chat exited without cleaning up its socket
		}
		if err != nil {
			return err
		}
		if reply.Member {
			if room != "" {
				return errors.New("your agent is in multiple chats; /kick it from one before posting")
			}
			room = path
		}
	}
	if room == "" {
		return errors.New("your agent is not in an open chat")
	}
	_, err = exchangePost(room, p)
	return err
}

func exchangePost(addr string, p post) (postReply, error) {
	var reply postReply
	conn, err := net.DialTimeout("unix", addr, 5*time.Second)
	if err != nil {
		return reply, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(p); err != nil {
		return reply, err
	}
	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		return reply, err
	}
	if reply.Error != "" {
		return reply, errors.New(reply.Error)
	}
	return reply, nil
}

func listenPosts(send func(tea.Msg), done <-chan struct{}) (func(), error) {
	root, err := roomsDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(root, "chat-")
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "chat.sock"))
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go readPost(conn, send, done)
		}
	}()
	return func() { listener.Close(); os.RemoveAll(dir) }, nil
}

func readPost(conn net.Conn, send func(tea.Msg), done <-chan struct{}) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var p post
	var result postReply
	if err := json.NewDecoder(conn).Decode(&p); err != nil {
		result.Error = err.Error()
	} else {
		reply := make(chan postReply, 1)
		send(postMsg{p, reply})
		select {
		case result = <-reply:
		case <-done:
			return
		case <-time.After(5 * time.Second):
			result.Error = "chat did not acknowledge the request"
		}
	}
	_ = json.NewEncoder(conn).Encode(result)
}
