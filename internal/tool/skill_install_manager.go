package tool

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// InstallManagerConfig wires the manager to the runtime.
type InstallManagerConfig struct {
	SkillsDir   string
	StagingDir  string
	VersionsDir string
	LedgerPath  string

	Limits            InstallLimits
	ProbeCLI          bool
	ProbeTimeout      time.Duration
	TrialBudget       time.Duration
	AllowedLocalRoots []string
	KeepVersions      int
	StagingTTL        time.Duration
}

// InstallManager owns the staging area and the ledger, and serializes work per
// skill name.
//
// Locking: nameLocks serializes Prepare/Confirm/Uninstall for one skill. It is
// deliberately not a single global lock — installs of unrelated skills should
// not block each other — but it does mean Confirm must re-run the collision
// check under the lock, because two Prepares can both have observed a clear
// field before either committed.
type InstallManager struct {
	cfg   InstallManagerConfig
	store *InstallStore

	mu        sync.Mutex
	nameLocks map[string]*sync.Mutex

	// reload is called after the skills tree changes on disk. It is set by the
	// owner (the agent) and must perform a full reload, not a partial one.
	reload func() (int, error)
	// registry/tools provide the live view used for collision prechecks.
	registry func() *SkillRegistry
	tools    func() *Registry
}

func NewInstallManager(cfg InstallManagerConfig) *InstallManager {
	if cfg.KeepVersions <= 0 {
		cfg.KeepVersions = 3
	}
	if cfg.StagingTTL <= 0 {
		cfg.StagingTTL = 24 * time.Hour
	}
	cfg.Limits = cfg.Limits.withDefaults()
	return &InstallManager{
		cfg:       cfg,
		store:     NewInstallStore(cfg.LedgerPath),
		nameLocks: map[string]*sync.Mutex{},
	}
}

// SetRuntime wires the live views the manager needs. reload must re-scan the
// skills tree; registry and tools supply the collision precheck.
func (m *InstallManager) SetRuntime(reload func() (int, error), registry func() *SkillRegistry, tools func() *Registry) {
	m.reload = reload
	m.registry = registry
	m.tools = tools
}

func (m *InstallManager) Store() *InstallStore { return m.store }

func (m *InstallManager) lockFor(name string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.nameLocks[name]; ok {
		return l
	}
	l := &sync.Mutex{}
	m.nameLocks[name] = l
	return l
}

func newInstallID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("inst-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b[:]))
}

func (m *InstallManager) stagingPath(id string) string {
	return filepath.Join(m.cfg.StagingDir, id)
}

// StageArchive copies uploaded bytes into a fresh staging directory and returns
// its install id. This must run synchronously inside the HTTP handler: the
// multipart temp files are removed the moment the handler returns.
func (m *InstallManager) StageArchive(filename string, read func(dst string) error) (string, *InstallRecord, error) {
	id := newInstallID()
	dir := m.stagingPath(id)
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0o700); err != nil {
		return "", nil, err
	}
	dst := filepath.Join(dir, "archive", "upload.bin")
	if err := read(dst); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	size := int64(0)
	if info, err := os.Stat(dst); err == nil {
		size = info.Size()
	}

	rec := &InstallRecord{
		InstallID: id,
		State:     InstallStaging,
		Mode:      "install",
		Source:    InstallSource{Kind: SourceArchive, Origin: filepath.Base(filename), Size: size},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return id, rec, m.writeStagedRecord(rec)
}

// StageLocalDir copies a server-local directory into staging.
//
// This source is an arbitrary-read primitive — whatever it copies in becomes
// readable by the model through skill_read — so it is refused unless the path
// sits under an explicitly configured root.
func (m *InstallManager) StageLocalDir(srcDir string) (string, *InstallRecord, error) {
	abs, err := filepath.Abs(srcDir)
	if err != nil {
		return "", nil, err
	}
	if err := m.checkLocalRoot(abs); err != nil {
		return "", nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("%s is not a directory", abs)
	}

	id := newInstallID()
	dir := m.stagingPath(id)
	extract := filepath.Join(dir, "extract", sanitizeName(filepath.Base(abs)))
	if err := os.MkdirAll(extract, 0o700); err != nil {
		return "", nil, err
	}
	if _, err := CopyLocalDir(abs, extract, m.cfg.Limits); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}

	rec := &InstallRecord{
		InstallID: id,
		State:     InstallStaging,
		Mode:      "install",
		Source:    InstallSource{Kind: SourcePath, Origin: abs},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return id, rec, m.writeStagedRecord(rec)
}

func (m *InstallManager) checkLocalRoot(abs string) error {
	if len(m.cfg.AllowedLocalRoots) == 0 {
		return fmt.Errorf("installing from a local path is disabled; set skills.install.allowed_local_roots to enable it")
	}
	// Never let the runtime home be a source: it holds config.json and tokens.
	for _, forbidden := range []string{m.cfg.SkillsDir, m.cfg.StagingDir, m.cfg.VersionsDir, filepath.Dir(m.cfg.LedgerPath)} {
		if forbidden == "" {
			continue
		}
		if within(abs, forbidden) {
			return fmt.Errorf("path %s is inside the LuckyAgent runtime directory", abs)
		}
	}
	for _, root := range m.cfg.AllowedLocalRoots {
		rootAbs, err := filepath.Abs(strings.TrimSpace(root))
		if err != nil || rootAbs == "" {
			continue
		}
		if within(abs, rootAbs) {
			return nil
		}
	}
	return fmt.Errorf("path %s is not under any configured allowed_local_roots", abs)
}

func within(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// Prepare runs extraction, the static scan, and the trial load for a staged
// install. It is the long pole: the probe stage spawns one subprocess per
// discovered subcommand, so callers run it in the background.
//
// Order is the whole point: nothing executes until the scan is clean.
func (m *InstallManager) Prepare(id string) (*InstallRecord, error) {
	rec, err := m.readStagedRecord(id)
	if err != nil {
		return nil, err
	}

	dir := m.stagingPath(id)
	extractRoot := filepath.Join(dir, "extract")

	if rec.Source.Kind == SourceArchive {
		rec.State = InstallStaging
		_ = m.writeStagedRecord(rec)
		if err := os.MkdirAll(extractRoot, 0o700); err != nil {
			return m.failStaged(rec, err)
		}
		archive := filepath.Join(dir, "archive", "upload.bin")
		if _, err := ExtractArchive(archive, extractRoot, m.cfg.Limits); err != nil {
			return m.rejectStaged(rec, err)
		}
		if _, err := NormalizeRoot(extractRoot, "staged-skill"); err != nil {
			return m.rejectStaged(rec, err)
		}
	}

	skillDir, err := soleChildDir(extractRoot)
	if err != nil {
		return m.rejectStaged(rec, err)
	}

	// Static pass. Executes nothing.
	rec.State = InstallScanning
	_ = m.writeStagedRecord(rec)
	report, err := ScanSkillDir(skillDir, m.cfg.Limits)
	if err != nil {
		return m.failStaged(rec, err)
	}
	rec.Scan = report
	rec.Digest = report.Digest

	if report.Blocked {
		rec.State = InstallRejected
		rec.Error = summarizeBlocks(report)
		_ = m.writeStagedRecord(rec)
		return rec, nil
	}

	// Trial load. The probe only runs now that the scan is clean.
	rec.State = InstallProbing
	_ = m.writeStagedRecord(rec)
	trial, err := TrialLoad(extractRoot, TrialOptions{
		ProbeCLI:     m.cfg.ProbeCLI,
		ProbeTimeout: m.cfg.ProbeTimeout,
		TotalBudget:  m.cfg.TrialBudget,
	})
	if err != nil {
		return m.rejectStaged(rec, err)
	}

	rec.Name = trial.Info.Name
	rec.Capability = trial.Capability
	rec.Tools = toolNames(trial.Capability)

	if !ValidSkillName(rec.Name) {
		return m.rejectStaged(rec, fmt.Errorf("skill name %q is not usable as a directory or URL segment", rec.Name))
	}

	// Collisions are advisory here and authoritative at Confirm; two concurrent
	// Prepares can both see a clear field.
	if collisions := m.collisions(trial.Capability); len(collisions) > 0 {
		rec.Capability.Collisions = collisions
		for _, c := range collisions {
			report.add("name_collision", SeverityBlock, "", c, 0)
		}
		rec.State = InstallRejected
		rec.Error = summarizeBlocks(report)
		_ = m.writeStagedRecord(rec)
		return rec, nil
	}

	rec.State = InstallReady
	rec.Error = ""
	return rec, m.writeStagedRecord(rec)
}

func (m *InstallManager) collisions(summary *CapabilitySummary) []string {
	var reg *SkillRegistry
	var tools *Registry
	if m.registry != nil {
		reg = m.registry()
	}
	if m.tools != nil {
		tools = m.tools()
	}
	return CheckCollisions(summary, reg, tools)
}

// Confirm commits a prepared install: snapshot any existing version, move the
// staged tree into place, update the ledger, then reload.
//
// The rename happens before the reload, never after. A rename is atomic, so a
// concurrent reload either sees the new skill or does not; the explicit reload
// that follows picks it up either way. Reversing the order could miss it.
func (m *InstallManager) Confirm(id string, acceptWarnings bool) (*InstallRecord, error) {
	rec, err := m.readStagedRecord(id)
	if err != nil {
		return nil, err
	}
	if !rec.CanConfirm() {
		return rec, fmt.Errorf("install %s is not ready to confirm (state: %s)", id, rec.State)
	}
	if rec.Scan != nil && rec.Scan.Warnings > 0 && !acceptWarnings {
		return rec, fmt.Errorf("install has %d warnings that must be accepted explicitly", rec.Scan.Warnings)
	}

	lock := m.lockFor(rec.Name)
	lock.Lock()
	defer lock.Unlock()

	// Re-check under the lock: another install of the same name may have landed
	// between Prepare and now.
	if collisions := m.collisions(rec.Capability); len(collisions) > 0 {
		rec.State = InstallRejected
		rec.Error = strings.Join(collisions, "; ")
		_ = m.writeStagedRecord(rec)
		return rec, fmt.Errorf("collision: %s", rec.Error)
	}

	staged, err := soleChildDir(filepath.Join(m.stagingPath(id), "extract"))
	if err != nil {
		return rec, err
	}
	live := filepath.Join(m.cfg.SkillsDir, rec.Name)

	// Remember whether an upgrade target was already enabled. A brand-new skill
	// must land disabled so its capabilities are opted into explicitly, but
	// silently disabling a skill the user was already running would be a footgun.
	wasEnabled := false
	if m.registry != nil {
		if reg := m.registry(); reg != nil {
			if meta, ok := reg.Get(rec.Name); ok && meta.State == SkillEnabled {
				wasEnabled = true
			}
		}
	}

	// Snapshot an existing install so the move has a free destination and so
	// rollback has something to restore.
	var snapshot string
	if _, err := os.Stat(live); err == nil {
		snapshot = filepath.Join(m.cfg.VersionsDir, rec.Name, fmt.Sprintf("%d", time.Now().UnixNano()))
		if err := MoveDir(live, snapshot); err != nil {
			return rec, fmt.Errorf("snapshot existing skill: %w", err)
		}
		rec.Mode = "upgrade"
	}

	if err := MoveDir(staged, live); err != nil {
		// Put the previous version back rather than leaving the skill missing.
		if snapshot != "" {
			_ = MoveDir(snapshot, live)
		}
		return rec, fmt.Errorf("install skill: %w", err)
	}

	now := time.Now()
	rec.State = InstallConfirmed
	rec.InstalledAt = now
	action := "installed"
	if snapshot != "" {
		action = "upgraded"
		rec.History = append(rec.History, InstallHistory{
			VersionID: filepath.Base(snapshot),
			Digest:    rec.Digest,
			Action:    action,
			At:        now,
		})
	}
	if err := m.mergeLedger(rec); err != nil {
		return rec, err
	}
	_ = m.writeStagedRecord(rec)
	_ = os.RemoveAll(m.stagingPath(id))

	if m.reload != nil {
		if _, err := m.reload(); err != nil {
			return rec, fmt.Errorf("skill installed but reload failed: %w", err)
		}
	}

	// The reload honors skills.auto_enable, which is about skills the user has
	// already accepted. A freshly installed one has not been accepted yet, so it
	// is walked back to registered — its tools stay in the registry but are not
	// callable and are not advertised to the model until an explicit enable.
	if !wasEnabled && m.registry != nil {
		if reg := m.registry(); reg != nil {
			if meta, ok := reg.Get(rec.Name); ok && meta.State == SkillEnabled {
				_ = reg.Disable(rec.Name)
			}
		}
	}

	m.pruneVersions(rec.Name)
	return rec, nil
}

// mergeLedger preserves an existing entry's history across an upgrade.
func (m *InstallManager) mergeLedger(rec *InstallRecord) error {
	if prev, ok := m.store.Get(rec.Name); ok && prev != nil {
		merged := append([]InstallHistory{}, prev.History...)
		rec.History = append(merged, rec.History...)
		if rec.InstalledAt.IsZero() {
			rec.InstalledAt = prev.InstalledAt
		}
	}
	return m.store.Upsert(rec)
}

// Abort discards a staged install.
func (m *InstallManager) Abort(id string) error {
	rec, err := m.readStagedRecord(id)
	if err == nil {
		rec.State = InstallAborted
		_ = m.writeStagedRecord(rec)
	}
	return os.RemoveAll(m.stagingPath(id))
}

// Uninstall removes a skill from the live tree, optionally snapshotting it first.
func (m *InstallManager) Uninstall(name string, purge bool) (string, error) {
	if !ValidSkillName(name) {
		return "", fmt.Errorf("invalid skill name")
	}
	lock := m.lockFor(name)
	lock.Lock()
	defer lock.Unlock()

	live := filepath.Join(m.cfg.SkillsDir, name)
	if _, err := os.Stat(live); err != nil {
		return "", fmt.Errorf("skill %s is not installed", name)
	}

	versionID := ""
	if purge {
		if err := os.RemoveAll(live); err != nil {
			return "", err
		}
	} else {
		versionID = fmt.Sprintf("%d", time.Now().UnixNano())
		if err := MoveDir(live, filepath.Join(m.cfg.VersionsDir, name, versionID)); err != nil {
			return "", err
		}
	}

	if rec, ok := m.store.Get(name); ok && rec != nil {
		if purge {
			_ = m.store.Remove(name)
		} else {
			rec.State = "uninstalled"
			rec.History = append(rec.History, InstallHistory{
				VersionID: versionID, Digest: rec.Digest, Action: "uninstalled", At: time.Now(),
			})
			_ = m.store.Upsert(rec)
		}
	}

	if m.reload != nil {
		if _, err := m.reload(); err != nil {
			return versionID, fmt.Errorf("skill removed but reload failed: %w", err)
		}
	}
	m.pruneVersions(name)
	return versionID, nil
}

// Versions lists the rollback snapshots available for a skill, newest first.
func (m *InstallManager) Versions(name string) []string {
	if !ValidSkillName(name) {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(m.cfg.VersionsDir, name))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// Rollback restores a snapshot, snapshotting whatever is currently live first so
// the operation is itself reversible.
func (m *InstallManager) Rollback(name, versionID string) error {
	if !ValidSkillName(name) || !isVersionID(versionID) {
		return fmt.Errorf("invalid skill name or version id")
	}
	lock := m.lockFor(name)
	lock.Lock()
	defer lock.Unlock()

	snapshot := filepath.Join(m.cfg.VersionsDir, name, versionID)
	if _, err := os.Stat(snapshot); err != nil {
		return fmt.Errorf("version %s not found for %s", versionID, name)
	}

	live := filepath.Join(m.cfg.SkillsDir, name)
	var displaced string
	if _, err := os.Stat(live); err == nil {
		displaced = filepath.Join(m.cfg.VersionsDir, name, fmt.Sprintf("%d", time.Now().UnixNano()))
		if err := MoveDir(live, displaced); err != nil {
			return err
		}
	}
	if err := MoveDir(snapshot, live); err != nil {
		if displaced != "" {
			_ = MoveDir(displaced, live)
		}
		return err
	}

	if rec, ok := m.store.Get(name); ok && rec != nil {
		rec.State = InstallConfirmed
		rec.History = append(rec.History, InstallHistory{
			VersionID: versionID, Action: "rolled_back", At: time.Now(),
		})
		_ = m.store.Upsert(rec)
	}

	if m.reload != nil {
		if _, err := m.reload(); err != nil {
			return fmt.Errorf("rolled back but reload failed: %w", err)
		}
	}
	return nil
}

// ListStaged returns staged installs that have not been committed or aborted.
func (m *InstallManager) ListStaged() []*InstallRecord {
	entries, err := os.ReadDir(m.cfg.StagingDir)
	if err != nil {
		return nil
	}
	var out []*InstallRecord
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if rec, err := m.readStagedRecord(e.Name()); err == nil {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (m *InstallManager) Staged(id string) (*InstallRecord, error) { return m.readStagedRecord(id) }

// GC drops expired staging directories and prunes old version snapshots.
func (m *InstallManager) GC() {
	entries, err := os.ReadDir(m.cfg.StagingDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-m.cfg.StagingTTL)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(m.cfg.StagingDir, e.Name()))
	}

	if versions, err := os.ReadDir(m.cfg.VersionsDir); err == nil {
		for _, v := range versions {
			if v.IsDir() {
				m.pruneVersions(v.Name())
			}
		}
	}
}

func (m *InstallManager) pruneVersions(name string) {
	versions := m.Versions(name)
	for i, v := range versions {
		if i < m.cfg.KeepVersions {
			continue
		}
		_ = os.RemoveAll(filepath.Join(m.cfg.VersionsDir, name, v))
	}
}

// ---- staged record persistence ----

func (m *InstallManager) recordPath(id string) string {
	return filepath.Join(m.stagingPath(id), "record.json")
}

func (m *InstallManager) writeStagedRecord(rec *InstallRecord) error {
	rec.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	path := m.recordPath(rec.InstallID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *InstallManager) readStagedRecord(id string) (*InstallRecord, error) {
	if !isInstallID(id) {
		return nil, fmt.Errorf("invalid install id")
	}
	data, err := os.ReadFile(m.recordPath(id))
	if err != nil {
		return nil, err
	}
	var rec InstallRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (m *InstallManager) rejectStaged(rec *InstallRecord, cause error) (*InstallRecord, error) {
	rec.State = InstallRejected
	rec.Error = cause.Error()
	if rec.Scan == nil {
		rec.Scan = &ScanReport{ScannedAt: time.Now()}
	}
	// Extraction rejections carry their own rule name; anything else is generic.
	var ee *extractError
	if errors.As(cause, &ee) {
		rec.Scan.add(ee.Rule, SeverityBlock, ee.Path, ee.Detail, 0)
	} else {
		rec.Scan.add("install_rejected", SeverityBlock, "", cause.Error(), 0)
	}
	return rec, m.writeStagedRecord(rec)
}

func (m *InstallManager) failStaged(rec *InstallRecord, cause error) (*InstallRecord, error) {
	rec.State = InstallFailed
	rec.Error = cause.Error()
	_ = m.writeStagedRecord(rec)
	return rec, cause
}

// ---- helpers ----

func soleChildDir(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	var dirs []string
	for _, e := range entries {
		// Skip dot-directories: SkillLoader ignores nothing, so any scratch
		// directory that lands here would otherwise read as a second skill.
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("expected exactly one skill directory in the staged tree, found %d", len(dirs))
	}
	return filepath.Join(root, dirs[0]), nil
}

func summarizeBlocks(report *ScanReport) string {
	blocks := report.BlockingFindings()
	if len(blocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, f := range blocks {
		if f.Path != "" {
			parts = append(parts, fmt.Sprintf("%s (%s)", f.Detail, f.Path))
			continue
		}
		parts = append(parts, f.Detail)
	}
	return strings.Join(parts, "; ")
}

func toolNames(summary *CapabilitySummary) []string {
	if summary == nil {
		return nil
	}
	out := make([]string, 0, len(summary.Tools))
	for _, t := range summary.Tools {
		out = append(out, t.Name)
	}
	return out
}

func isInstallID(id string) bool {
	if !strings.HasPrefix(id, "inst-") || len(id) > 64 {
		return false
	}
	for _, r := range id[len("inst-"):] {
		if r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func isVersionID(v string) bool {
	if v == "" || len(v) > 32 {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
