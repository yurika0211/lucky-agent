# Memory hygiene

Aestus exposes deterministic hygiene directly on `memory.Store`; there is no agent tool wrapper in this repository.

```go
report := store.AuditHygiene(memory.HygieneOptions{
    MinSeverity: "medium",
    MaxFindings: 50,
})
```

Use `QuarantineDirty` to archive findings for review, `DeleteDirty` for an explicit physical delete, and `RestoreHygiene` to restore entries previously tagged by hygiene. Pass `IncludeInactive` when archived, superseded, expired, or future dated notes should also be inspected.

Rules cover empty notes, raw `User:`/`Assistant:` transcripts, secret-like strings, prompt injection text, expiry, low confidence long term entries, duplicates, state conflicts, and oversized content. Findings include severity, reason, score, path, and a short preview. `AnalyzeMemoryContent` can validate content before saving.
