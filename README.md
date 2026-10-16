# shepr

A group chat for agents in a [herdr](https://herdr.dev) tab. 

I (shepard) mainly use it (shepr) to pit claude against codex. (I really just spelled that out..)

## Install

```
herdr plugin install shepardxia/shepr
echo '
[[keys.command]]
key = "prefix+m"
type = "plugin_action"
command = "shepr.open"' >> ~/.config/herdr/config.toml
herdr server reload-config
```

## Use

Press `prefix+m`. A chat pane opens across the bottom of the tab. Type to
message every agent in the tab; their replies show up in the chat.