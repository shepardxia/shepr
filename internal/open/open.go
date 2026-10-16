// Package open is the open action: it focuses the tab's chat pane, or opens
// one and docks it across the bottom of the tab.
package open

import (
	"github.com/shepardxia/shepr/internal/herdr"

	"encoding/json"
	"errors"
	"os"
	"strings"
)

// chatTitle is the [[panes]] title in herdr-plugin.toml, which herdr shows as
// the pane's label.
const chatTitle = "shepr chat"

// Run focuses the tab's chat pane, or opens one under the calling pane and
// docks it across the bottom of the tab.
func Run() error {
	tab, caller, ws := os.Getenv("HERDR_TAB_ID"), os.Getenv("HERDR_PANE_ID"), os.Getenv("HERDR_WORKSPACE_ID")
	if tab == "" {
		return errors.New("shepr open runs as a herdr action inside a tab")
	}
	if ws == "" {
		ws, _, _ = strings.Cut(tab, ":")
	}
	id, err := chatPane(tab)
	if err != nil {
		return err
	}
	if id == "" {
		params := map[string]any{"plugin_id": "shepr", "entrypoint": "chat", "placement": "split", "direction": "down", "focus": false}
		if caller != "" {
			params["target_pane_id"] = caller
		}
		raw, err := herdr.Call("plugin.pane.open", params)
		if err != nil {
			return err
		}
		var out struct {
			PluginPane struct {
				Pane struct {
					PaneID string `json:"pane_id"`
				} `json:"pane"`
			} `json:"plugin_pane"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		id = out.PluginPane.Pane.PaneID
		if err := dock(ws, tab, id); err != nil {
			return err
		}
	}
	_, err = herdr.Call("plugin.pane.focus", map[string]any{"pane_id": id})
	return err
}

func chatPane(tab string) (string, error) {
	raw, err := herdr.Call("pane.list", map[string]any{})
	if err != nil {
		return "", err
	}
	var out struct {
		Panes []struct {
			PaneID string `json:"pane_id"`
			TabID  string `json:"tab_id"`
			Label  string `json:"label"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	for _, p := range out.Panes {
		if p.TabID == tab && p.Label == chatTitle {
			return p.PaneID, nil
		}
	}
	return "", nil
}
