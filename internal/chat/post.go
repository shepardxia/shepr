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
	"github.com/shepardxia/shepr/internal/herdr"
)

// A request without content queries membership and supported post formats.
type post struct {
	Pane   string   `json:"pane"`
	Name   string   `json:"name,omitempty"`
	Text   string   `json:"text,omitempty"`
	Images []string `json:"images,omitempty"`
}

const postTimeout = 5 * time.Second

func (p post) timeout() time.Duration {
	if len(p.Images) > 0 {
		return time.Minute
	}
	return postTimeout
}

type postReply struct {
	Member         bool   `json:"member"`
	SupportsImages bool   `json:"supports_images,omitempty"`
	Error          string `json:"error,omitempty"`
}

type postMsg struct {
	message message
	reply   chan postReply
}

func roomsDir() (string, error) {
	root, err := os.UserCacheDir()
	return filepath.Join(root, "shepr"), err
}

func parsePost(args []string) (post, error) {
	usage := errors.New("usage: shepr post <name> [--image <path>]... [--] [message]")
	if len(args) < 2 {
		return post{}, usage
	}
	p := post{Name: args[0]}
	args = args[1:]
	for len(args) > 0 && args[0] == "--image" {
		if len(args) < 2 || args[1] == "" {
			return post{}, usage
		}
		path, err := filepath.Abs(args[1])
		if err != nil {
			return post{}, err
		}
		p.Images = append(p.Images, path)
		args = args[2:]
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	p.Text = strings.Join(args, " ")
	if strings.TrimSpace(p.Text) == "" && len(p.Images) == 0 {
		return post{}, usage
	}
	return p, nil
}

// Post finds the caller's room before sending the message to it once.
func Post(args []string) error {
	p, err := parsePost(args)
	if err != nil {
		return err
	}
	agent, err := herdr.GetAgent(p.Name)
	if err != nil {
		return err
	}
	p.Pane, p.Name = agent.PaneID, agent.Handle()
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
			if len(p.Images) > 0 && !reply.SupportsImages {
				return errors.New("reopen this chat to enable image posts")
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
	conn, err := net.DialTimeout("unix", addr, postTimeout)
	if err != nil {
		return reply, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(p.timeout()))
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

func newRoomDir() (string, error) {
	root, err := roomsDir()
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	return os.MkdirTemp(root, "chat-")
}

func listenPosts(dir string, send func(tea.Msg), done <-chan struct{}) (func(), error) {
	listener, err := net.Listen("unix", filepath.Join(dir, "chat.sock"))
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go readPost(dir, conn, send, done)
		}
	}()
	return func() { listener.Close() }, nil
}

func readPost(dir string, conn net.Conn, send func(tea.Msg), done <-chan struct{}) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(postTimeout))
	var p post
	var result postReply
	defer func() { _ = json.NewEncoder(conn).Encode(result) }()
	if err := json.NewDecoder(conn).Decode(&p); err != nil {
		result.Error = err.Error()
		return
	}
	_ = conn.SetDeadline(time.Now().Add(p.timeout()))
	msg, err := p.message(dir)
	if err != nil {
		result.Error = err.Error()
		return
	}
	reply := make(chan postReply, 1)
	send(postMsg{message: msg, reply: reply})
	select {
	case result = <-reply:
	case <-done:
		result.Error = "chat closed before acknowledging the request"
	case <-time.After(postTimeout):
		result.Error = "chat did not acknowledge the request"
	}
}

// Resolve paths off the UI loop. Publish the entire message only once all
// attachments are readable.
func (p post) message(dir string) (message, error) {
	msg := message{pane: p.Pane, author: p.Name, text: p.Text}
	for _, path := range p.Images {
		img, err := fileImage(dir, path)
		if err != nil {
			return message{}, err
		}
		msg.images = append(msg.images, img)
	}
	return msg, nil
}

func (c *chat) acceptPost(msg message) postReply {
	a := c.agents[msg.pane]
	reply := postReply{Member: a != nil && a.member, SupportsImages: true}
	if msg.text == "" && len(msg.images) == 0 {
		return reply
	}
	if !reply.Member {
		reply.Error = "your agent is not in this room"
	} else if err := c.receive(msg); err != nil {
		reply.Error = err.Error()
	}
	return reply
}
