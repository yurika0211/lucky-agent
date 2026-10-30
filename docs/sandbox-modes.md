# Agent sandbox modes

`/set` changes the mode used by new turns. A running turn keeps the snapshot
it captured when it started.

| Mode | Workspace | Network and external tools |
| --- | --- | --- |
| `dev` | Host session directory | Existing development behavior |
| `workspace-write` | Original session directory, with `.git` metadata protected | Bubblewrap boundary, no network, MCP/plugin execution disabled |
| `iso` | Bounded disposable copy | Bubblewrap boundary, no network, edits are discarded when the turn ends |

Use `/set workspace-write` for ordinary coding work. A restricted mode requires
the session to have an explicit working directory; it never falls back to the
server process directory. `iso` also rejects a workspace before copying when it
exceeds the byte, file, or symlink caps. The default mode remains `dev`.

The `workspace-write` mode is provided by `luckyagent-tools/sandbox` and is
selected through the runtime sandbox alias. External callers should use the
published module version selected by the launcher instead of a local replace.
