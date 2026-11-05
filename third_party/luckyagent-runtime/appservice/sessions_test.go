package appservice

import (
	"errors"
	"testing"
)

type sessionFixture struct {
	items     []SessionInfo
	byID      map[string]*testSession
	deleted   []string
	deleteErr error
}

func (f *sessionFixture) Get(id string) (*testSession, bool) {
	value, ok := f.byID[id]
	return value, ok
}
func (f *sessionFixture) ListInfo() []SessionInfo { return f.items }
func (f *sessionFixture) Delete(id string) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

func TestSessionLookupPrecedenceAndAmbiguity(t *testing.T) {
	fixture := &sessionFixture{byID: map[string]*testSession{}}
	for _, info := range []SessionInfo{
		{ID: "alpha-one", Title: "Build Notes"}, {ID: "alpha-two", Title: "Other"},
		{ID: "beta", Title: "  alpha  "}, {ID: "gamma", Title: "Build Notes Later"},
		{ID: "2", Title: "Numeric ID"},
	} {
		fixture.items = append(fixture.items, info)
		fixture.byID[info.ID] = &testSession{ID: info.ID, Title: info.Title}
	}
	service := NewSessions(fixture, nil)
	for _, test := range []struct {
		query  string
		mode   SessionMatchMode
		status SessionMatchStatus
		id     string
	}{
		{"alpha-one", MatchSessionTitle, SessionMatched, "alpha-one"},
		{"alpha", MatchSessionTitle, SessionAmbiguous, ""},
		{"  BUILD   notes ", MatchSessionTitle, SessionMatched, "alpha-one"},
		{"notes", MatchSessionTitle, SessionAmbiguous, ""},
		{"Build Notes", MatchSessionIDPrefix, SessionNotFound, ""},
		{"beta", MatchSessionID, SessionMatched, "beta"},
		{"bet", MatchSessionID, SessionNotFound, ""},
		{"2", MatchSessionReference, SessionMatched, "2"},
		{"1", MatchSessionReference, SessionMatched, "alpha-one"},
	} {
		got := service.Find(test.query, test.mode)
		if got.Status != test.status || got.ID != test.id {
			t.Errorf("Find(%q,%v) = %+v", test.query, test.mode, got)
		}
	}
	var missing *sessionFixture
	if got := NewSessions(missing, nil).Find("x", MatchSessionTitle); got.Status != SessionNotFound {
		t.Fatal(got)
	}
}

func TestSessionDeleteProtectsActiveAndForgetsOnlyAfterSuccess(t *testing.T) {
	failed := errors.New("storage failure")
	fixture := &sessionFixture{deleteErr: failed}
	var forgotten []string
	service := NewSessions(fixture, func(id string) { forgotten = append(forgotten, id) })
	if err := service.Delete("active", "active"); err == nil || len(fixture.deleted) != 0 {
		t.Fatal("active session reached storage")
	}
	if err := service.Delete("old", ""); !errors.Is(err, failed) || len(forgotten) != 0 {
		t.Fatal("failed deletion cleared memory")
	}
	fixture.deleteErr = nil
	if err := service.Delete("old", ""); err != nil || len(forgotten) != 1 || forgotten[0] != "old" {
		t.Fatalf("successful delete: %v %v", err, forgotten)
	}
}

func TestSessionMetadataRejectsInvalidTitleBeforeMutation(t *testing.T) {
	sess := &testSession{Title: "original"}
	empty := "  "
	pinned := true
	if err := UpdateSessionMetadata(sess, SessionMetadata{Title: &empty, Pinned: &pinned}); err == nil {
		t.Fatal("empty title accepted")
	}
	if pinned, _ := sess.Organization(); pinned || sess.Title != "original" {
		t.Fatal("invalid patch mutated session")
	}
}

type testSession struct {
	ID, Title, project         string
	pinned                     bool
	saveErr                    error
	fullWrites, metadataWrites int
}

func (s *testSession) SetTitle(value string)        { s.Title = value }
func (s *testSession) SetPinned(value bool)         { s.pinned = value }
func (s *testSession) SetProject(value string)      { s.project = value }
func (s *testSession) Save() error                  { s.fullWrites++; return s.saveErr }
func (s *testSession) SaveMetadata() error          { s.metadataWrites++; return s.saveErr }
func (s *testSession) Organization() (bool, string) { return s.pinned, s.project }

func TestSessionWritesUseRequestedPersistenceAndReturnFailures(t *testing.T) {
	failed := errors.New("disk full")
	sess := &testSession{saveErr: failed}
	if err := RenameSession(sess, "new name"); !errors.Is(err, failed) || sess.Title != "new name" || sess.fullWrites != 1 {
		t.Fatalf("rename did not use full save: %+v %v", sess, err)
	}
	title, project, pinned := " trimmed ", "project", true
	if err := UpdateSessionMetadata(sess, SessionMetadata{Title: &title, Project: &project, Pinned: &pinned}); !errors.Is(err, failed) || sess.metadataWrites != 1 || sess.fullWrites != 1 || sess.Title != "trimmed" || !sess.pinned || sess.project != project {
		t.Fatalf("metadata persistence: %+v %v", sess, err)
	}
	var missing *testSession
	if RenameSession(missing, "name") == nil || UpdateSessionMetadata(missing, SessionMetadata{Title: &title}) == nil {
		t.Fatal("typed nil session accepted")
	}
}
