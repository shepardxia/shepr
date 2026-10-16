// Package herdr is the part of herdr's socket API shepr uses.
package herdr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Error is a refusal from herdr, such as agent_blocked.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Call sends one request over herdr's socket; herdr answers one line and
// closes the connection.
func Call(method string, params any) (json.RawMessage, error) {
	path := os.Getenv("HERDR_SOCKET_PATH")
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "herdr", "herdr.sock")
	}
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	req, _ := json.Marshal(map[string]any{"id": "shepr", "method": method, "params": params})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// Agent is the part of herdr's agent record shepr reads.
type Agent struct {
	Kind   string `json:"agent"`
	Name   string `json:"name"`
	Status string `json:"agent_status"`
	PaneID string `json:"pane_id"`
	TabID  string `json:"tab_id"`
}

// Handle is how an agent appears in the chat: its herdr name, else its kind.
func (a Agent) Handle() string {
	if a.Name != "" {
		return a.Name
	}
	return a.Kind
}

// Agents lists every live agent.
func Agents() ([]Agent, error) {
	raw, err := Call("agent.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Agents []Agent `json:"agents"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out.Agents, nil
}

// Prompt submits text and Enter to the agent in pane.
func Prompt(pane, text string) error {
	_, err := Call("agent.prompt", map[string]any{"target": pane, "text": text})
	return err
}

// Rename sets the herdr name of the agent in pane.
func Rename(pane, name string) error {
	_, err := Call("agent.rename", map[string]any{"target": pane, "name": name})
	return err
}
