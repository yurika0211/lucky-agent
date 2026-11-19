# Agent sandbox modes

`/set` changes the mode used by new turns. A running turn keeps the snapshot
it captured when it started.

| Mode | Workspace | Terminal boundary |
| --- | --- | --- |
| `dev` | Host session directory | Host `sh -c` as the process user. No bubblewrap, no nsjail, no extra namespace. |
| `workspace-write` | Original session directory, with `.git` metadata protected | Bubblewrap boundary, no network. File, MCP, and GUI tools are not inside that jail; restricted mode disables many of them. |
| `iso` | Bounded disposable copy | Bubblewrap boundary, no network, edits are discarded when the turn ends. File, MCP, and GUI tools are not inside that jail. |

Missing `runtime/security_mode.json` means `dev`. The default stays `dev` for
the local REPL and for unattended CLI, HTTP, Telegram, and QQ turns. Those
surfaces run host commands until an operator uses `/set workspace-write` or
`/set iso`. A reply that only says "sandboxed" is wrong while the mode is `dev`.

`/set workspace-write` is the ordinary coding boundary. A restricted mode
requires the session to have an explicit working directory; it never falls back
to the server process directory. `iso` also rejects a workspace before copying
when it exceeds the byte, file, or symlink caps.

A substring deny of paths such as `.ssh/` or `/etc/shadow` is a command-text
check. It is not an OS jail, and `dev` does not treat it as one. Do not
describe that check as a sandbox.

The `workspace-write` mode is provided by `luckyagent-tools/sandbox` and is
selected through the runtime sandbox alias. External callers should use the
published module version selected by the launcher instead of a local replace.
