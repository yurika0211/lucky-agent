# LuckyAgent Rename Status

Historical checklist. The names below are the current modules as of 2026-10-20.
The old single module `github.com/yurika0211/luckyagent` and the in-repo `UI/`
tree are no longer in this repository.

## Current State

- Launcher module: `github.com/yurika0211/lucky-agent` (`cmd/la`)
- Agent module: `github.com/yurika0211/lucky-agent/agent`
- App, runtime, tools, API, gateway and plugins are separate modules
- Runtime home: `${HOME}/.luckyagent`
- Database file: `luckyagent.db`
- gRPC service name remains `luckyagent.LuckyAgentService`
- Proto and generated gRPC code live in `luckyagent-api` / `luckyagent-app/api/grpc`

## Compatibility Kept

- `lh` remains the CLI command name.
- `LH_*` environment variables remain valid.
- `LUCKYAGENT_MANUAL_FILE` is preferred. `LUCKYHARNESS_MANUAL_FILE` and
  `description/AGENTS.md` are still fallback paths in `agent/system_prompt.go`.
  `lh init` writes the default manual to `memory/prompts/AGENTS.md`.

## Verification

```bash
make test
GOWORK=off go test -mod=readonly ./...
(cd agent && GOWORK=off go test -mod=readonly -race ./...)
```
