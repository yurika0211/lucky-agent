# Aestus memory architecture

Aestus keeps memory inspectable on disk and treats Markdown notes as the durable source of truth. The design was extracted from LuckyAgent and keeps its Obsidian folder names, frontmatter, wikilinks, aliases, temporal fields, and block IDs.

## Recall pipeline

```text
query
  -> lexical and alias terms
  -> direct memory activation
  -> optional wikilink/backlink/tag graph spread
  -> optional tidal rerank
  -> ordered SearchResult values
```

`Store.Search` uses `DefaultActivationOptions`, including one graph hop by default. Call `Activate` directly when a caller needs score components and graph paths. `Route` uses a bounded, read only activation path and returns typed constraints, risks, required tools, and temporal notes from matching `RoutePolicy` values.

## Short and mid term state

`ShortTermBuffer` is volatile process state. It retains a bounded window of `ConversationTurn` values and emits a structured summary when messages overflow the window. `Message` is deliberately local and provider neutral so callers can convert the context to any chat API.

`MidTermStore` writes structured `SessionSummary` records as Markdown. Use a directory such as `filepath.Join(vaultDir, "30_Sessions")` and a bounded summary count. `GenerateSessionSummary` provides a deterministic extractor for topics, decisions, questions, and code context.

## Tidal reranker

The tidal component is optional and conservative. It is a post recall reranker, so a store remains useful without it and the base activation score remains dominant. A typical setup is:

```go
tidalStore, err := memory.OpenTidalStore("./.aestus/runtime/tidal_memory.db")
if err != nil { /* handle error */ }
defer tidalStore.Close()

reranker, err := memory.NewPersistentTidalMemoryReranker(
    memory.DefaultTidalRerankerConfig(), tidalStore,
)
if err != nil { /* handle error */ }
store.SetActivationReranker(reranker)
```

The SQLite tables record query, recall, feedback, and learned response kernel data. They are runtime telemetry, not the durable memory vault. Applications can call `ObserveFeedback` on the reranker and keep the telemetry path entirely optional.

## Hygiene and migration

Hygiene checks are deterministic and inspect empty content, raw conversation fragments, secret-like strings, prompt injection text, expiry, low confidence long term entries, duplicates, conflicting state, and oversized content. Quarantine archives a note for review; deletion physically removes it. Graph migration and note rename operations are explicit so applications can dry run before mutating a vault.

## Research provenance

The original LuckyAgent design notes discuss a tidal or response-kernel model. Aestus implements the practical minimum: feedback driven coarse age buckets applied after recall. It does not claim that the research hypotheses are proven.
