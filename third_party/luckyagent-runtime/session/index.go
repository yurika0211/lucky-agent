package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const sessionIndexName = "index.db"

// sessionIndex is a derived catalog of session sidecars. Transcripts and
// blobs stay in their files. A missing or unreadable database falls back to
// the directory scan and must not hide a session.
type sessionIndex struct {
	db *sql.DB
}

type indexedSession struct {
	Meta       sessionMetadata
	SourceSize int64
	SourceTime time.Time
	TailOffset int64
	TailStart  int
	FileSize   int64
}

func openSessionIndex(dir string) (*sessionIndex, error) {
	path := filepath.Join(dir, sessionIndexName)
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	index := &sessionIndex{db: db}
	if err := index.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return index, nil
}

func (idx *sessionIndex) init() error {
	_, err := idx.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			agent_role TEXT NOT NULL DEFAULT '',
			agent_system_prompt TEXT NOT NULL DEFAULT '',
			pinned INTEGER NOT NULL DEFAULT 0,
			project TEXT NOT NULL DEFAULT '',
			message_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			byte_size INTEGER NOT NULL DEFAULT 0,
			format TEXT NOT NULL DEFAULT '',
			shell_context TEXT NOT NULL DEFAULT '{}',
			source_size INTEGER NOT NULL DEFAULT 0,
			source_mtime_ns INTEGER NOT NULL DEFAULT 0,
			tail_offset INTEGER NOT NULL DEFAULT 0,
			tail_start INTEGER NOT NULL DEFAULT 0,
			file_size INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_sessions_list ON sessions(pinned, updated_at);
	`)
	if err != nil {
		return err
	}
	// Upgrade existing derived catalogs without touching transcripts. Old rows
	// default to an absent recap and remain valid until the next metadata write.
	rows, err := idx.db.Query("PRAGMA table_info(sessions)")
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, column := range []struct{ name, ddl string }{
		{"recap", "TEXT NOT NULL DEFAULT ''"},
		{"recap_updated_at", "TEXT NOT NULL DEFAULT ''"},
		{"tail_start", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if !columns[column.name] {
			if _, err := idx.db.Exec("ALTER TABLE sessions ADD COLUMN " + column.name + " " + column.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

func (idx *sessionIndex) Close() error {
	if idx == nil || idx.db == nil {
		return nil
	}
	return idx.db.Close()
}

func (idx *sessionIndex) List() (map[string]indexedSession, error) {
	if idx == nil || idx.db == nil {
		return nil, fmt.Errorf("session index is closed")
	}
	rows, err := idx.db.Query(`
		SELECT id, title, agent_role, agent_system_prompt, pinned, project, message_count,
			created_at, updated_at, byte_size, format, shell_context,
			source_size, source_mtime_ns, tail_offset, tail_start, file_size, recap, recap_updated_at
		FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]indexedSession)
	for rows.Next() {
		var row indexedSession
		var pinned int
		var created, updated, shell, recapUpdated string
		var sourceMTime int64
		if err := rows.Scan(
			&row.Meta.ID, &row.Meta.Title, &row.Meta.AgentRole, &row.Meta.AgentSystemPrompt,
			&pinned, &row.Meta.Project, &row.Meta.MessageCount, &created, &updated,
			&row.Meta.ByteSize, &row.Meta.Format, &shell,
			&row.SourceSize, &sourceMTime, &row.TailOffset, &row.TailStart, &row.FileSize,
			&row.Meta.Recap, &recapUpdated,
		); err != nil {
			return nil, err
		}
		row.Meta.Pinned = pinned != 0
		row.Meta.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		row.Meta.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		if at, err := time.Parse(time.RFC3339Nano, recapUpdated); err == nil {
			row.Meta.RecapUpdatedAt = &at
		}
		_ = json.Unmarshal([]byte(shell), &row.Meta.ShellContext)
		row.SourceTime = time.Unix(0, sourceMTime)
		out[row.Meta.ID] = row
	}
	return out, rows.Err()
}

func (idx *sessionIndex) Upsert(row indexedSession) error {
	if idx == nil || idx.db == nil {
		return nil
	}
	shell, err := json.Marshal(row.Meta.ShellContext)
	if err != nil {
		return err
	}
	pinned := 0
	if row.Meta.Pinned {
		pinned = 1
	}
	recapUpdated := ""
	if row.Meta.RecapUpdatedAt != nil {
		recapUpdated = row.Meta.RecapUpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err = idx.db.Exec(`
		INSERT INTO sessions (
			id, title, agent_role, agent_system_prompt, pinned, project, message_count,
			created_at, updated_at, byte_size, format, shell_context,
			source_size, source_mtime_ns, tail_offset, tail_start, file_size, recap, recap_updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			recap = excluded.recap,
			recap_updated_at = excluded.recap_updated_at,
			agent_role = excluded.agent_role,
			agent_system_prompt = excluded.agent_system_prompt,
			pinned = excluded.pinned,
			project = excluded.project,
			message_count = excluded.message_count,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			byte_size = excluded.byte_size,
			format = excluded.format,
			shell_context = excluded.shell_context,
			source_size = excluded.source_size,
			source_mtime_ns = excluded.source_mtime_ns,
			tail_offset = excluded.tail_offset,
			tail_start = excluded.tail_start,
			file_size = excluded.file_size
	`, row.Meta.ID, row.Meta.Title, row.Meta.AgentRole, row.Meta.AgentSystemPrompt,
		pinned, row.Meta.Project, row.Meta.MessageCount,
		row.Meta.CreatedAt.UTC().Format(time.RFC3339Nano),
		row.Meta.UpdatedAt.UTC().Format(time.RFC3339Nano),
		row.Meta.ByteSize, row.Meta.Format, string(shell),
		row.SourceSize, row.SourceTime.UnixNano(), row.TailOffset, row.TailStart, row.FileSize, row.Meta.Recap, recapUpdated)
	return err
}

func (idx *sessionIndex) Delete(id string) error {
	if idx == nil || idx.db == nil {
		return nil
	}
	_, err := idx.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func sourceFingerprint(path string) (int64, time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, time.Time{}, false
	}
	return info.Size(), info.ModTime(), true
}

func indexRowFresh(row indexedSession, size int64, mod time.Time) bool {
	return row.SourceSize == size && row.SourceTime.Equal(mod)
}
