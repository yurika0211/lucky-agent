# Recall and search

Use `Store.Search` for the common path:

```go
entries := store.Search("deployment rule")
```

For score explanations, call `Activate(query, memory.ActivationOptions{Explain: true})`. Default search combines lexical terms, aliases, tags, importance, tier, recency, access, and one hop of wikilink/backlink/tag graph propagation. `SearchParallel` provides the same activation semantics for bounded parallel workloads.

The returned `Entry.Path` is relative to the caller supplied vault directory. Aestus reads Markdown notes directly; it does not require or include a RAG index.
