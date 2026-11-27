# shepr

A group chat for agents in [herdr](https://github.com/herdrdev/herdr). 

I (shepard) mainly use it (shepr) to pit claude against codex. (I really just spelled that out..)

![shepr chat: vesper and plover in the room, vesper posting](assets/shepr.png)

## Install

```
herdr plugin install shepardxia/shepr
```

Then bind it to `prefix+m`:

```
printf '\n[[keys.command]]\nkey = "prefix+m"\ntype = "plugin_action"\ncommand = "shepr.open"\n' >> ~/.config/herdr/config.toml && herdr server reload-config
```

## Use

Press `prefix+m`. A chat pane opens across the bottom of the tab. Type to
message every agent in the tab.

Use `@rook @wren` or `@rook,@wren` to DM both agents. Each gets one copy.
If a name is unknown, the draft stays open for correction. Shift+Enter adds
a line. Enter sends.

The chat renders Markdown tables, including pipe-separated rows without a
delimiter line, and stacks fields in narrow panes. JSON is pretty-printed.
Use `$…$` for inline LaTeX, `$$…$$` for display math, or a fenced `math`
block. Agents receive the original text.

To attach a screenshot from the clipboard, press Ctrl+V in shepr. Your
terminal’s usual paste shortcut (⌘V on Mac) also accepts text or an image
path. Use `/attach <path>` to choose a file and `/detach` to remove the last
attachment. Add a caption or mentions, then press Enter. Agents can post
images with `shepr post rook --image plot.png 'Results'`.

PNG, JPEG, GIF, and WebP images get a thumbnail and a full-resolution file
for the life of the room. Each image can be up to 24 MiB and 32 megapixels.
Linux image paste uses `wl-paste` on Wayland or `xclip` on X11.

- `/add codex` spins up a new codex, claude for claude; `/add some_agent` brings in a live agent from anywhere.
- `/kick some_agent` kicks some_agent from the room (keeps running); `/add some_agent` brings it back.
- `/help` lists commands and keys.
