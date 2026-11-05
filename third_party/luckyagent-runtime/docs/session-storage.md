# Session storage and recovery

## User-facing recaps

Optional `recap` and `recap_updated_at` metadata contain the Agent-generated
one-sentence preview. They are separate from titles, compact summaries, memory
and RAG. Old sessions omit both fields. List/search and cold session handles
read the stored preview without loading history; sidecars remain authoritative
and the SQLite catalog migrates automatically, including existing databases.

`RecapSnapshot` captures recent transcript evidence with an update timestamp.
`UpdateRecap` only accepts a nonempty result for that same live session version;
late results after a new message or deletion are discarded. Failed writes keep
the previous preview. Recap writes do not change `updated_at`, title or messages.
No configuration keys are added.

The `session` package owns durable conversation history. Sessions retain the
existing metadata sidecars, `segment_v1` JSONL messages, external bodies and
legacy Markdown migration. No new configuration fields are required.

Loaded transcripts stay resident only while in use. After Save, idle sessions
are unloaded back to metadata stubs (default: at most 8 fully loaded sessions,
2-minute idle). Unsaved or rewrite-pending sessions are never dropped.
`ResidentStats` reports how many transcripts currently occupy the heap.

## Concurrent operations and deletion

Append, Save, Clear, Roll, restore, migration, blob externalization and deletion
share a per-session lifecycle lock. Normal Save copies and persists only the
unsaved message suffix; structural replacements force an atomic full rewrite.
A successful Save cannot replace messages appended concurrently with an older
snapshot. Blob externalization works on a copy instead of changing an unlocked
message slice.

`Manager.Delete` reports filesystem failures. The manager retains the handle
when deletion fails, allowing the application to report the failure and retry.
Files may already be partly removed when a later deletion step fails; deletion
is not a transaction across all files. After success, the removed handle is
marked deleted. Save, history reads and maintenance reject it with
`ErrSessionDeleted`; a late response cannot recreate the deleted transcript.
An explicit later `Manager.Ensure(id)` creates a distinct live handle. The old
handle cannot overwrite it. Applications should cancel tasks and queued work
bound to the deleted handle as well.

Use `GetMessagesWithError`, `AddProviderMessageWithError` or
`AddMessageWithError` when errors must reach a caller. The existing methods
remain source-compatible wrappers, so callers using those older signatures
still cannot receive load or append errors directly.

## Backups and body collection

New backups use format version 2 and embed every referenced blob body, with
content hashes checked when creating and restoring a backup. A backup can
restore its bodies after the live transcript directory is lost. Version 1
backups remain readable; a missing referenced body makes restore fail before
replacing the current history. The backup itself remains unchanged.

Restore resets the persisted message count, invalidates page offsets and
forces a full transcript rewrite. Reloading the manager after restore yields
the restored messages, including after a Roll changed the previous transcript.

Blob GC first reads references from live history, archives and all backups.
Unreadable, corrupt or incomplete reference sources stop collection before any
body is deleted. Archive parsing is strict even for a partial final record.
Backups are outside the live transcript directory and retain their existing
lifecycle.

## Interrupted JSONL appends

A newline commits a JSONL record. Readers accept a syntactically incomplete
final record without a newline only when it does not remove any message
already counted by durable metadata. Earlier complete records remain readable;
read-only history and pagination never modify the damaged file. Committed
invalid records, middle corruption and missing previously recorded messages
return an error instead of an empty conversation.

The next append removes an incomplete uncommitted tail before writing new
messages. A complete legacy final record without a newline receives a
separator. Failed appends attempt to truncate back to the pre-append boundary;
rollback failures are returned together with the original error. These rules
cover interrupted writes within one manager; they do not provide a lock for
multiple processes writing the same session directory.

## Metadata and large histories

`SaveMetadata` only updates the metadata sidecar. Pinning, renaming, grouping,
shell context and profile changes can remain cold without loading the message
body. Loading history later preserves those edited fields. Unsaved in-memory
messages still require Save; a metadata update does not commit them.

ListInfo reads cached storage sizes rather than walking session directories on
every list request. Storage operations refresh the cache; listing alone does
not detect arbitrary external filesystem changes.

The first cold segment page validates and counts the transcript, building a
sparse offset index. Further pages seek near the requested range instead of
decoding the entire file again. The immutable index stores no message bodies,
has at most 8192 offsets and is rebuilt when file identity, size or modification
time changes. Message history stays unloaded during paging. Legacy Markdown
keeps the existing bounded tail cache. Full-history operations such as model
context preparation can still load the complete conversation.

## Session catalog

`sessions/index.db` is a derived SQLite catalog. It stores the list fields from
the metadata sidecar, the source file size and modification time, and the byte
offset of the latest 500-message window. Append takes that position from the
bytes just written. When the window moves into older records, Save reads only
the messages that left the window, starting at the recorded offset. A cold
latest page whose file size still matches starts at that offset. It does not
store message bodies, blobs, or other files from the session directory. The
sidecar and transcript remain the recovery source. Older databases gain the
window ordinal through `tail_start`; a missing column is added on open.

Save, SaveMetadata and Delete update the row after the file change succeeds.
An index write failure does not undo that file change. Startup reads the table
when the recorded source fingerprint still matches the file. A changed or
missing source is read from the file and the row is corrected or removed.
A missing, corrupt or unreadable database falls back to the existing directory
scan, so sessions stay visible. The database is not a configuration field and
is separate from the RAG store.

A cold page uses the recorded offset only when the requested range lies inside
the latest 500 messages and the file size still matches. An older page or a
damaged record still scans from the head and rebuilds the in-memory offset
index. A file whose size no longer matches the recorded window is treated as
an external replacement: the next read drops the offset, scans the replacement,
and publishes the rebuilt index. A same-size transcript that is shorter than
the sidecar count still fails instead of hiding a lost message. A full rewrite
records the new window while it encodes the replacement, so the old byte
positions are not reused. Legacy Markdown keeps its existing tail cache and
does not require this offset.
