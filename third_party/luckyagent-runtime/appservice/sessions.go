// Package appservice provides reusable session, model and configuration
// operations. Storage, schema rules and runtime execution are injected ports.
package appservice

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type SessionInfo struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Recap          string     `json:"recap,omitempty"`
	RecapUpdatedAt *time.Time `json:"recap_updated_at,omitempty"`
	AgentRole      string     `json:"agent_role,omitempty"`
	Pinned         bool       `json:"pinned,omitempty"`
	Project        string     `json:"project,omitempty"`
	MessageCount   int        `json:"message_count"`
	ByteSize       int64      `json:"byte_size,omitempty"`
	Format         string     `json:"format,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// S is the caller's session handle, normally a pointer. The zero value denotes
// a missing session; the module never reads or owns session message contents.
type SessionStore[S comparable] interface {
	Get(string) (S, bool)
	ListInfo() []SessionInfo
	Delete(string) error
}

type SessionMatchMode int

const (
	MatchSessionID SessionMatchMode = iota
	MatchSessionIDPrefix
	MatchSessionTitle
	// MatchSessionReference also accepts a one-based list index after exact ID.
	MatchSessionReference
)

type SessionMatchStatus int

const (
	SessionNotFound SessionMatchStatus = iota
	SessionMatched
	SessionAmbiguous
)

type SessionMatch[S comparable] struct {
	Status  SessionMatchStatus
	Session S
	ID      string
	Matches []SessionInfo
}

type Sessions[S comparable] struct {
	store  SessionStore[S]
	forget func(string)
}

func NewSessions[S comparable](store SessionStore[S], forget func(string)) *Sessions[S] {
	if absent(store) {
		store = nil
	}
	return &Sessions[S]{store: store, forget: forget}
}

// Find stops on ambiguity. Precedence is exact ID, optional list index, ID
// prefix, exact title, title prefix, title substring.
func (s *Sessions[S]) Find(query string, mode SessionMatchMode) SessionMatch[S] {
	query = strings.TrimSpace(query)
	if s == nil || s.store == nil || query == "" {
		return SessionMatch[S]{}
	}
	var zero S
	if sess, ok := s.store.Get(query); ok && sess != zero {
		return SessionMatch[S]{Status: SessionMatched, Session: sess, ID: query}
	}
	if mode == MatchSessionID {
		return SessionMatch[S]{}
	}
	infos := s.store.ListInfo()
	if mode == MatchSessionReference {
		if index, err := strconv.Atoi(query); err == nil && index > 0 && index <= len(infos) {
			id := infos[index-1].ID
			if sess, ok := s.store.Get(id); ok && sess != zero {
				return SessionMatch[S]{Status: SessionMatched, Session: sess, ID: id}
			}
		}
	}
	match := func(predicate func(SessionInfo) bool) SessionMatch[S] {
		var matches []SessionInfo
		for _, info := range infos {
			if predicate(info) {
				matches = append(matches, info)
			}
		}
		if len(matches) > 1 {
			return SessionMatch[S]{Status: SessionAmbiguous, Matches: matches}
		}
		if len(matches) == 1 {
			if sess, ok := s.store.Get(matches[0].ID); ok && sess != zero {
				return SessionMatch[S]{Status: SessionMatched, Session: sess, ID: matches[0].ID}
			}
		}
		return SessionMatch[S]{}
	}
	if found := match(func(info SessionInfo) bool { return strings.HasPrefix(info.ID, query) }); found.Status != SessionNotFound {
		return found
	}
	if mode == MatchSessionIDPrefix {
		return SessionMatch[S]{}
	}
	normalize := func(text string) string { return strings.ToLower(strings.Join(strings.Fields(text), " ")) }
	query = normalize(query)
	for _, predicate := range []func(string) bool{
		func(title string) bool { return title == query },
		func(title string) bool { return title != "" && strings.HasPrefix(title, query) },
		func(title string) bool { return title != "" && strings.Contains(title, query) },
	} {
		if found := match(func(info SessionInfo) bool { return predicate(normalize(info.Title)) }); found.Status != SessionNotFound {
			return found
		}
	}
	return SessionMatch[S]{}
}

func (s *Sessions[S]) ResolveIDPrefix(query string) (S, error) {
	var zero S
	query = strings.TrimSpace(query)
	if query == "" {
		return zero, fmt.Errorf("session id is required")
	}
	result := s.Find(query, MatchSessionIDPrefix)
	switch result.Status {
	case SessionMatched:
		return result.Session, nil
	case SessionAmbiguous:
		ids := make([]string, 0, len(result.Matches))
		for _, info := range result.Matches {
			ids = append(ids, info.ID)
		}
		return zero, fmt.Errorf("session id prefix %q is ambiguous: %s", query, strings.Join(ids, ", "))
	default:
		return zero, fmt.Errorf("session not found: %s", query)
	}
}

// Delete forgets short-term memory only after successful storage deletion.
func (s *Sessions[S]) Delete(id, protectedID string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("session manager is unavailable")
	}
	if id == "" {
		return fmt.Errorf("session id is required")
	}
	if protectedID != "" && id == protectedID {
		return fmt.Errorf("cannot delete the active session")
	}
	if err := s.store.Delete(id); err != nil {
		return err
	}
	if s.forget != nil {
		s.forget(id)
	}
	return nil
}

type SessionRenamer interface {
	SetTitle(string)
	Save() error
}

func RenameSession(sess SessionRenamer, title string) error {
	if absent(sess) {
		return fmt.Errorf("session not found")
	}
	sess.SetTitle(title)
	return sess.Save()
}

type SessionMetadata struct {
	Title   *string
	Pinned  *bool
	Project *string
}

type SessionMetadataWriter interface {
	SetTitle(string)
	SetPinned(bool)
	SetProject(string)
	SaveMetadata() error
}

// UpdateSessionMetadata validates before mutating and avoids rewriting history.
func UpdateSessionMetadata(sess SessionMetadataWriter, patch SessionMetadata) error {
	if patch.Title == nil && patch.Pinned == nil && patch.Project == nil {
		return fmt.Errorf("no session fields to update")
	}
	if patch.Title != nil && strings.TrimSpace(*patch.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if absent(sess) {
		return fmt.Errorf("session not found")
	}
	if patch.Title != nil {
		sess.SetTitle(strings.TrimSpace(*patch.Title))
	}
	if patch.Pinned != nil {
		sess.SetPinned(*patch.Pinned)
	}
	if patch.Project != nil {
		sess.SetProject(*patch.Project)
	}
	return sess.SaveMetadata()
}
