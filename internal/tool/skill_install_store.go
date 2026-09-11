package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// InstallStore persists the ledger of managed skills. Skills copied into
// skills/ by hand have no entry; that absence is what marks them unmanaged, so
// the UI knows not to offer rollback for them.
type InstallStore struct {
	mu   sync.Mutex
	path string
}

func NewInstallStore(path string) *InstallStore {
	return &InstallStore{path: path}
}

func (s *InstallStore) Path() string { return s.path }

// Load reads the ledger. A missing file is not an error — it means nothing has
// been installed through the pipeline yet.
func (s *InstallStore) Load() (*Ledger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *InstallStore) loadLocked() (*Ledger, error) {
	empty := &Ledger{Version: 1, Skills: map[string]*InstallRecord{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return empty, nil
		}
		return nil, err
	}
	var ledger Ledger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return nil, err
	}
	if ledger.Skills == nil {
		ledger.Skills = map[string]*InstallRecord{}
	}
	if ledger.Version == 0 {
		ledger.Version = 1
	}
	return &ledger, nil
}

// Save writes the ledger atomically: a crash mid-write must not leave a
// truncated file that would read as "nothing is managed".
func (s *InstallStore) Save(ledger *Ledger) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(ledger)
}

func (s *InstallStore) saveLocked(ledger *Ledger) error {
	if ledger.Skills == nil {
		ledger.Skills = map[string]*InstallRecord{}
	}
	ledger.Version = 1
	ledger.Updated = time.Now()

	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Get returns the ledger entry for a skill.
func (s *InstallStore) Get(name string) (*InstallRecord, bool) {
	ledger, err := s.Load()
	if err != nil {
		return nil, false
	}
	rec, ok := ledger.Skills[name]
	return rec, ok
}

// Upsert replaces one skill's record under a single lock, so concurrent installs
// of different skills cannot lose each other's writes through a read-modify-write
// race.
func (s *InstallStore) Upsert(rec *InstallRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ledger, err := s.loadLocked()
	if err != nil {
		return err
	}
	rec.UpdatedAt = time.Now()
	ledger.Skills[rec.Name] = rec
	return s.saveLocked(ledger)
}

// Remove drops a skill's record.
func (s *InstallStore) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ledger, err := s.loadLocked()
	if err != nil {
		return err
	}
	delete(ledger.Skills, name)
	return s.saveLocked(ledger)
}

// List returns every managed record.
func (s *InstallStore) List() []*InstallRecord {
	ledger, err := s.Load()
	if err != nil {
		return nil
	}
	out := make([]*InstallRecord, 0, len(ledger.Skills))
	for _, rec := range ledger.Skills {
		out = append(out, rec)
	}
	return out
}
