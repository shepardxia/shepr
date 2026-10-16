package open

import (
	"encoding/json"
	"fmt"

	"github.com/shepardxia/shepr/internal/herdr"
)

// chatShare is the part of the tab's height the chat strip takes.
const chatShare = 0.3

// node is one node of herdr's layout tree: a pane, or a split whose ratio is
// its first child's share.
type node struct {
	Type      string  `json:"type"`
	PaneID    string  `json:"pane_id"`
	Direction string  `json:"direction"`
	Ratio     float64 `json:"ratio"`
	First     *node   `json:"first"`
	Second    *node   `json:"second"`
}

// lead is the subtree's first pane in reading order.
func (n *node) lead() string {
	for n.Type != "pane" {
		n = n.First
	}
	return n.PaneID
}

func (n *node) panes() []string {
	if n.Type == "pane" {
		return []string{n.PaneID}
	}
	return append(n.First.panes(), n.Second.panes()...)
}

// without is the tree after one pane leaves it, its split collapsed as herdr
// collapses it.
func (n *node) without(id string) *node {
	if n.Type == "pane" {
		if n.PaneID == id {
			return nil
		}
		return n
	}
	first, second := n.First.without(id), n.Second.without(id)
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	c := *n
	c.First, c.Second = first, second
	return &c
}

// moveTo moves a live pane and returns the tab it created, if any.
func moveTo(pane string, dest map[string]any) (string, error) {
	raw, err := herdr.Call("pane.move", map[string]any{"pane_id": pane, "destination": dest, "focus": false})
	if err != nil {
		return "", err
	}
	var out struct {
		Result struct {
			Changed bool   `json:"changed"`
			Reason  string `json:"reason"`
			Created struct {
				TabID string `json:"tab_id"`
			} `json:"created_tab"`
		} `json:"move_result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if !out.Result.Changed {
		return "", fmt.Errorf("move %s: %s", pane, out.Result.Reason)
	}
	return out.Result.Created.TabID, nil
}

// dock rebuilds the tab with the chat as a strip under every other pane. herdr
// splits only leaves and refuses moves within a tab, so all panes but one go
// out to a holding tab and come back in tree order; a move keeps the pane's
// process, id and scrollback. A zoomed tab keeps the plain split.
func dock(ws, tab, chat string) error {
	raw, err := herdr.Call("layout.export", map[string]any{"tab_id": tab})
	if err != nil {
		return err
	}
	var out struct {
		Layout struct {
			Zoomed bool  `json:"zoomed"`
			Root   *node `json:"root"`
		} `json:"layout"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if out.Layout.Zoomed || out.Layout.Root == nil {
		return nil
	}
	rest := out.Layout.Root.without(chat)
	if rest == nil {
		return nil
	}
	anchor := rest.lead()
	away := map[string]bool{}
	hold := ""
	for _, p := range append(rest.panes()[1:], chat) {
		dest := map[string]any{"type": "tab", "tab_id": hold, "split": "right"}
		if hold == "" {
			dest = map[string]any{"type": "new_tab", "workspace_id": ws, "label": "shepr"}
		}
		created, err := moveTo(p, dest)
		if err != nil {
			return restore(tab, anchor, away, err)
		}
		if hold == "" {
			hold = created
		}
		away[p] = true
	}
	back := func(p, target, direction string, ratio float64) error {
		_, err := moveTo(p, map[string]any{"type": "tab", "tab_id": tab, "target_pane_id": target, "split": direction, "ratio": ratio})
		if err == nil {
			delete(away, p)
		}
		return err
	}
	if err := back(chat, anchor, "down", 1-chatShare); err != nil {
		return restore(tab, anchor, away, err)
	}
	if err := rebuild(rest, back); err != nil {
		return restore(tab, anchor, away, err)
	}
	return nil
}

// rebuild recreates a subtree whose lead pane is in place: the second child's
// lead splits the first child's, then each child is rebuilt in its own cell.
func rebuild(n *node, back func(p, target, direction string, ratio float64) error) error {
	if n.Type == "pane" {
		return nil
	}
	if err := back(n.Second.lead(), n.First.lead(), n.Direction, n.Ratio); err != nil {
		return err
	}
	if err := rebuild(n.First, back); err != nil {
		return err
	}
	return rebuild(n.Second, back)
}

// restore brings panes left in the holding tab back beside anchor, unarranged,
// so a failed dock never loses one.
func restore(tab, anchor string, away map[string]bool, cause error) error {
	for p := range away {
		_, _ = moveTo(p, map[string]any{"type": "tab", "tab_id": tab, "target_pane_id": anchor, "split": "right"})
	}
	return fmt.Errorf("dock: %w", cause)
}
