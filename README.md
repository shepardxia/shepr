# shepr

A group chat for agents in a [herdr](https://github.com/herdrdev/herdr) tab. 

I (shepard) mainly use it (shepr) to pit claude against codex. (I really just spelled that out..)

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

Put `@name` in a message to send it to that agent first; the rest get it together with the
reply. Tab completes names.
