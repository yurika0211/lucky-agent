# Remember and save

Save a durable fact or rule with an explicit category and tier:

```go
err := store.SaveWithOptions(
    "Run tests before deployment.",
    "rule",
    memory.TierLong,
    0.9,
    memory.SaveOptions{
        Tags:  []string{"release"},
        Links: []string{"Deployment"},
    },
)
```

`Save`, `SaveWithTier`, `SaveWithMetadata`, and `SaveWithOptionsResult` are convenience variants. The latter reports whether a note was created or merged into an existing duplicate. Optional metadata includes tags, Obsidian links and aliases, status, validity and expiry, state keys, confidence, superseded IDs, and typed route policies.

Use a caller supplied vault path such as `./.aestus/memory` or `$HOME/.aestus/memory`; Aestus never assumes a LuckyAgent home directory. Avoid storing credentials, transient chat transcripts, or information that should not persist.
