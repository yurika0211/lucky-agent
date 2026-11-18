# LuckyAgent core

The public Go module `github.com/yurika0211/lucky-agent/agent` owns Agent
execution: the loop, context selection and packing, tool/approval policy,
session-aware execution, memory-use rules, convergence and streaming events.
It has its own `go.mod` so the application can depend on it independently of
the repository's executable launcher.

Backends, model protocols, session storage, RAG, configuration and default
resource preparation come from `luckyagent-runtime`. The full product's CLI,
HTTP/channel commands and embedding setup come from `luckyagent-app`. This core
module never imports the application module.

## Explicit injection

```go
inputs := &agent.Inputs{
    Config: manager, Provider: model, Sessions: sessions, Tools: tools,
}
instance, err := agent.NewWithInputs(agent.Options{}, inputs)
if err != nil {
    return err
}
defer instance.Close()
```

`NewWithInputs` borrows the supplied resources by default. To transfer ownership,
set `Options.TakeOwnership` and provide `Inputs.CloseResources`. The caller
retains responsibility if construction fails. After successful transfer,
`instance.Close()` calls the supplied cleanup once.

The minimum injected resources are configuration, a provider, a session manager,
and a tool registry. Memory stores/summaries, RAG, tool services, delegate,
autonomy, cron, MCP and media backends are optional. Missing optional engines are
not constructed or registered. The core supplies in-memory context accounting,
a tool gateway and metrics when these are absent. This reduces runtime setup;
the existing Go module dependency graph still includes the full adapters.

For a full runtime bundle prepared by `runtime/bootstrap.Open`, use
`agent.InputsFromComponents(resources)` to map all configured services and their
cleanup into these same inputs. Construction and activation are separate.
Call `resources.Start(ctx, instance.StartupHooks())` to start configured services.
The existing `NewWithDependencies` entry point accepts the original runtime
bundle and uses the same adapter.

Request budgeting and completion review have independent, storage-free service
boundaries. Each turn captures its provider, input budget and explicitly required
tools; subsequent requests in that turn keep that selection. Optional scheduler
and child-task binding lives separately from core dependency validation.

`New` and `NewWithMemory` remain compatibility convenience constructors for
the default application lifecycle. New embeddings should use explicit
dependencies; full applications can use `luckyagent-app/bootstrap.New`.
The existing shared `config.Manager` and concrete public runtime types remain
compatible inputs; this migration does not invent a second configuration schema.

## Validate independently

[Long-task context](../docs/long-task-context.md) documents the shared context
budget, bounded recoverable history, host-bound evidence reader and execution
ledger. [Packing details](../docs/prompts/la-context-packing-strategy.md) describe
the message order and trimming priorities.

```bash
GOWORK=off go test -mod=readonly -race ./...
GOWORK=off go vet ./...
```

The parent directory's `go test ./...` skips this nested module. Root `make test`
and CI run both modules explicitly. The import-boundary test prevents this core
from depending on the application or former host-internal paths.
