# Embed SDK (in-process)

Import LuckyAgent as a Go library and run the agent inside your process.

```go
import "github.com/yurika0211/luckyagent/sdk"

agent, err := sdk.New(sdk.Config{
    HomeDir:  "/var/lib/myapp/luckyagent",
    Provider: "openai",
    Model:    "gpt-5.4-mini",
    APIKey:   os.Getenv("OPENAI_API_KEY"),
})
if err != nil {
    return err
}
defer agent.Close()

out, err := agent.Chat(ctx, "hello")
```

## Notes

- This is an **Embed SDK**, not an HTTP client for `lh serve`.
- Always pass an app-specific `HomeDir` so you do not overwrite the CLI profile under `~/.luckyagent`.
- Do not import `internal/*` from application code; use `sdk` only.
- Cancel in-flight turns by canceling the `context.Context` passed to `Chat*` / RAG helpers.

## v0 surface

| Area | API |
|------|-----|
| Lifecycle | `New`, `Close`, `HomeDir` |
| Chat | `Chat`, `ChatSession`, `ChatStream`, `ChatSessionStream` |
| Sessions | `NewSession`, `NewSessionWithTitle`, `ListSessions`, `GetSession`, `RenameSession`, `DeleteSession` |
| Memory | `Remember`, `RememberLongTerm`, `Recall` |
| RAG | `IndexText`, `IndexFile`, `IndexDirectory`, `SearchRAG`, `RemoveDocument`, `ListDocuments`, `RAGStats` |
| Tools | `RegisterTool`, `UnregisterTool`, `EnableTool`, `DisableTool`, `ListTools` |
| Model | `SwitchModel`, `CurrentModel`, `ListModels` |

Streaming `Event` values may carry optional `Approval`, `Observation`, and `Usage` payloads when the runtime emits them.

### Config knobs

- `SystemPrompt` — write an embed-local SOUL.md
- `AutoApprove` — auto-approve gated tools inside the host process
- `DisableTools` — disable builtins after bootstrap (e.g. `terminal`)
- `LoadExisting` — load previous HomeDir settings, then apply overrides

### Register a host tool

```go
err := agent.RegisterTool(sdk.ToolSpec{
    Name:        "order_lookup",
    Description: "Look up an order by id",
    Parameters: map[string]sdk.ToolParam{
        "order_id": {Type: "string", Description: "Order id", Required: true},
    },
    AutoApprove: true,
    Handler: func(args map[string]any) (string, error) {
        id, _ := args["order_id"].(string)
        return lookupOrder(id)
    },
})
```

### Index host knowledge

```go
_, err := agent.IndexText(ctx, "docs:faq", "FAQ", faqMarkdown)
hits, err := agent.SearchRAG(ctx, "refund policy", &sdk.RAGSearchOptions{TopK: 5})
```

## Example

```bash
go run ./examples/embed_minimal
```
