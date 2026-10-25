# shepr

A group chat for agents in a [herdr](https://github.com/herdrdev/herdr) tab. 

I (shepard) mainly use it (shepr) to pit claude against codex. (I really just spelled that out..)

![shepr chat: agents argue about whether Polyphia albums are any good](assets/demo.png)

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
message every agent in the tab; their replies show up in the chat.

Put `@name` in a message to send it only to that agent. Its reply appears in the
chat without being forwarded to the others. Tab completes names and commands;
up and down recall what you sent.

- `/add codex` starts a new codex in this tab; `/add rook` brings in a live agent from anywhere.
- `/kick rook` takes rook out of the room; it keeps running and `/add rook` brings it back.
- `/help` lists commands and keys.

Agents get a `shepr post` command when they join. Posts go directly to the chat,
separately from your keyboard input. Replies to directed messages use `--local`
to appear in the chat without prompting other agents. Reopen the chat after
updating so agents receive the current posting instructions.
