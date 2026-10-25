// Package herdr is the part of herdr's socket API shepr uses.
package herdr

import (
	"bufio"
	"encoding/json"
	"errors"
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

// Code is the code of the herdr refusal in err, empty for any other error.
func Code(err error) string {
	var he *Error
	if errors.As(err, &he) {
		return he.Code
	}
	return ""
}

// Agent is the part of herdr's agent record shepr reads.
type Agent struct {
	Kind   string `json:"agent"`
	Name   string `json:"name"`
	Status string `json:"agent_status"`
	PaneID string `json:"pane_id"`
	TabID  string `json:"tab_id"`
	// LastTurn places the agent's latest finished turn in herdr's event
	// sequence; it grows with every turn the agent finishes.
	LastTurn uint64 `json:"completion_seq"`
}

// Handle is how an agent appears in the chat: its herdr name, else its kind.
func (a Agent) Handle() string {
	if a.Name != "" {
		return a.Name
	}
	return a.Kind
}

// get calls method and decodes its result into out.
func get(method string, params, out any) error {
	raw, err := Call(method, params)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// Agents lists every live agent.
func Agents() ([]Agent, error) {
	var out struct {
		Agents []Agent `json:"agents"`
	}
	err := get("agent.list", map[string]any{}, &out)
	return out.Agents, err
}

// Pane is the part of herdr's pane record shepr reads.
type Pane struct {
	PaneID string `json:"pane_id"`
	TabID  string `json:"tab_id"`
	Label  string `json:"label"`
	Cwd    string `json:"cwd"`
}

// Panes lists every pane.
func Panes() ([]Pane, error) {
	var out struct {
		Panes []Pane `json:"panes"`
	}
	err := get("pane.list", map[string]any{}, &out)
	return out.Panes, err
}

// Rect is a pane's place in its tab, in cells.
type Rect struct {
	PaneID string
	Width  int
	Height int
}

// Layout is every pane in the tab that holds pane, with its size.
func Layout(pane string) ([]Rect, error) {
	var out struct {
		Layout struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				Rect   struct {
					Width  int `json:"width"`
					Height int `json:"height"`
				} `json:"rect"`
			} `json:"panes"`
		} `json:"layout"`
	}
	if err := get("pane.layout", map[string]any{"pane_id": pane}, &out); err != nil {
		return nil, err
	}
	rects := make([]Rect, len(out.Layout.Panes))
	for i, p := range out.Layout.Panes {
		rects[i] = Rect{p.PaneID, p.Rect.Width, p.Rect.Height}
	}
	return rects, nil
}

// Split opens a shell pane beside target, without focus, and returns it.
func Split(target, direction, cwd string) (string, error) {
	var out struct {
		Pane Pane `json:"pane"`
	}
	err := get("pane.split", map[string]any{"target_pane_id": target, "direction": direction, "cwd": cwd, "focus": false}, &out)
	return out.Pane.PaneID, err
}

// ClosePane closes pane and ends its process.
func ClosePane(pane string) error {
	_, err := Call("pane.close", map[string]any{"pane_id": pane})
	return err
}

// Start launches an agent of kind under name in pane, a shell at its prompt.
// herdr checks kind before pane, so an unsupported kind fails on any pane.
func Start(name, kind, pane string) error {
	_, err := Call("agent.start", map[string]any{"name": name, "kind": kind, "pane_id": pane})
	return err
}

// Prompt submits text and Enter to the agent in pane and waits until the
// agent takes it up. It fails with agent_prompt_stalled when the agent shows
// no activity, as when input reaches an agent that is still starting.
func Prompt(pane, text string) error {
	wait := map[string]any{"until": []string{"working", "blocked"}, "timeout_ms": 20000}
	_, err := Call("agent.prompt", map[string]any{"target": pane, "text": text, "wait": wait})
	return err
}

// Rename sets the herdr name of the agent in pane.
func Rename(pane, name string) error {
	_, err := Call("agent.rename", map[string]any{"target": pane, "name": name})
	return err
}
