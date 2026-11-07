# Tool module and Agent integration

`luckyagent-tools` retains tool contracts, registry, builtins, Skills, MCP tool
adapters and execution packages such as hitl, skillroute, execpolicy, compact,
workflow, cron and autonomy. These packages do not import the Agent core.

The public Agent is `github.com/yurika0211/lucky-agent/agent`. Its runtime-owned
adapters for tool choice, context, approvals and convergence stay with the core.
Actual model, session, memory and RAG implementations are public packages in
`luckyagent-runtime`; the full product's CLI/API/channel operations and default
assembly live in `luckyagent-app`.

`luckyagent-runtime/tool` retains small aliases and the host credential backend
adapter. MCP connection lifecycle is in runtime/mcpclient. The Aestus SDK remains
inside runtime/memoryaestus; public memory contracts come from tools/memory.
Tool command parsing and HTTP/platform rendering stay outside the Agent.

New embedding code can prepare runtime components and pass them to
`agent.NewWithDependencies`. Default full applications use app/bootstrap.New.
Resource activation is explicit and ownership is transferred only after a
successful core construction.

Use published tools/runtime/Agent/app versions for independent builds. The
portable go.work selects this repository's root and Agent. An optional ignored
go.local.work can select external checkouts for joint development; it must not
leak machine-specific paths into committed manifests. Test each changed module,
then verify `GOWORK=off go build -o lh ./cmd/la` in the launcher repository.

The migration adds no tool name or runtime configuration key. Existing JSON and
wire fields remain unchanged. See [Agent core and launcher modules](core-modules.md).
