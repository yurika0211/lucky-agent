package tool

import "time"

// Severity levels for a ScanFinding. A block finding cannot be overridden by the
// user; a warn finding can be accepted explicitly at confirm time.
const (
	SeverityInfo  = "info"
	SeverityWarn  = "warn"
	SeverityBlock = "block"
)

// Install states, as surfaced to the API and UI.
const (
	InstallStaging   = "staging"
	InstallScanning  = "scanning"
	InstallProbing   = "probing"
	InstallReady     = "ready"
	InstallRejected  = "rejected"
	InstallConfirmed = "confirmed"
	InstallAborted   = "aborted"
	InstallFailed    = "failed"
)

// Install source kinds.
const (
	SourceArchive = "archive"
	SourcePath    = "path"
)

// reservedSkillNames are URL path segments under /api/v1/skills/ that address a
// sub-collection rather than a skill. A skill claiming one of these would be
// unreachable, so installs are refused.
var reservedSkillNames = map[string]struct{}{
	"install":  {},
	"installs": {},
	"reload":   {},
	"read":     {}, // would collide with the built-in skill_read tool
}

// InstallLimits bounds extraction and scanning. Zero fields fall back to
// DefaultInstallLimits, so a partially-populated struct is still safe.
type InstallLimits struct {
	MaxArchiveBytes      int64
	MaxUncompressedBytes int64
	MaxSingleFileBytes   int64
	MaxFiles             int
	MaxCompressionRatio  int
	MaxPathDepth         int
	MaxPathLen           int
	BlockOnSecrets       bool
}

// DefaultInstallLimits mirrors config.SkillInstallConfig's defaults.
func DefaultInstallLimits() InstallLimits {
	return InstallLimits{
		MaxArchiveBytes:      32 << 20,
		MaxUncompressedBytes: 64 << 20,
		MaxSingleFileBytes:   16 << 20,
		MaxFiles:             2000,
		MaxCompressionRatio:  100,
		MaxPathDepth:         12,
		MaxPathLen:           200,
	}
}

// withDefaults fills zero fields from DefaultInstallLimits. Callers build limits
// from user config, where "unset" must not mean "unbounded".
func (l InstallLimits) withDefaults() InstallLimits {
	d := DefaultInstallLimits()
	if l.MaxArchiveBytes <= 0 {
		l.MaxArchiveBytes = d.MaxArchiveBytes
	}
	if l.MaxUncompressedBytes <= 0 {
		l.MaxUncompressedBytes = d.MaxUncompressedBytes
	}
	if l.MaxSingleFileBytes <= 0 {
		l.MaxSingleFileBytes = d.MaxSingleFileBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = d.MaxFiles
	}
	if l.MaxCompressionRatio <= 0 {
		l.MaxCompressionRatio = d.MaxCompressionRatio
	}
	if l.MaxPathDepth <= 0 {
		l.MaxPathDepth = d.MaxPathDepth
	}
	if l.MaxPathLen <= 0 {
		l.MaxPathLen = d.MaxPathLen
	}
	return l
}

// ScanFinding is one rule hit. Path is relative to the staged skill root.
type ScanFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Detail   string `json:"detail"`
}

// ScanReport is the result of the static pass over a staged skill.
type ScanReport struct {
	Findings  []ScanFinding `json:"findings"`
	Files     int           `json:"files"`
	Bytes     int64         `json:"bytes"`
	Blocked   bool          `json:"blocked"`
	Warnings  int           `json:"warnings"`
	Digest    string        `json:"digest"`
	ScannedAt time.Time     `json:"scanned_at"`
}

// add records a finding and keeps the derived counters in sync.
func (r *ScanReport) add(rule, severity, path, detail string, line int) {
	r.Findings = append(r.Findings, ScanFinding{
		Rule: rule, Severity: severity, Path: path, Detail: detail, Line: line,
	})
	switch severity {
	case SeverityBlock:
		r.Blocked = true
	case SeverityWarn:
		r.Warnings++
	}
}

// BlockingFindings returns only the findings that make an install impossible.
func (r *ScanReport) BlockingFindings() []ScanFinding {
	var out []ScanFinding
	for _, f := range r.Findings {
		if f.Severity == SeverityBlock {
			out = append(out, f)
		}
	}
	return out
}

// CapabilityTool describes one tool the staged skill would contribute.
type CapabilityTool struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	ExposeToModel bool     `json:"expose_to_model"`
	Command       []string `json:"command,omitempty"`
	// Origin records how the tool was derived: "declared" (a ## Tools section),
	// "script" (one tool per file in scripts/), or "cli_probe" (scraped from
	// `<script> --help`, which means the skill's code was executed).
	Origin string `json:"origin"`
	// FullName is the name the tool would occupy in the tool Registry.
	FullName string `json:"full_name"`
}

// CapabilitySummary is what a user reviews before confirming an install.
type CapabilitySummary struct {
	Name        string           `json:"name"`
	Aliases     []string         `json:"aliases,omitempty"`
	Description string           `json:"description,omitempty"`
	Summary     string           `json:"summary,omitempty"`
	Scripts     []string         `json:"scripts,omitempty"`
	Tools       []CapabilityTool `json:"tools"`
	Probed      bool             `json:"probed"`
	ProbeErrors []string         `json:"probe_errors,omitempty"`
	Collisions  []string         `json:"collisions,omitempty"`
}

// InstallSource records where a staged skill came from.
type InstallSource struct {
	Kind   string `json:"kind"`             // archive | path
	Origin string `json:"origin,omitempty"` // filename or local directory
	Size   int64  `json:"size,omitempty"`
}

// InstallHistory is one entry in a skill's rollback history.
type InstallHistory struct {
	VersionID string    `json:"version_id"`
	Digest    string    `json:"digest,omitempty"`
	Action    string    `json:"action"` // installed | upgraded | uninstalled | rolled_back
	At        time.Time `json:"at"`
}

// InstallRecord is a staged or committed install. The staging copy lives at
// <staging>/<install-id>/record.json; the committed copy lives in the ledger.
type InstallRecord struct {
	InstallID string `json:"install_id"`
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Digest    string `json:"digest,omitempty"`
	State     string `json:"state"`
	Mode      string `json:"mode"` // install | upgrade
	Error     string `json:"error,omitempty"`

	Source     InstallSource      `json:"source"`
	Scan       *ScanReport        `json:"scan,omitempty"`
	Capability *CapabilitySummary `json:"capability,omitempty"`

	Tools       []string         `json:"tools,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	InstalledAt time.Time        `json:"installed_at,omitempty"`
	History     []InstallHistory `json:"history,omitempty"`
}

// CanConfirm reports whether the staged install may be committed.
func (r *InstallRecord) CanConfirm() bool {
	if r == nil || r.State != InstallReady {
		return false
	}
	return r.Scan == nil || !r.Scan.Blocked
}

// Ledger is the on-disk record of managed skills. Skills installed by hand (by
// copying a directory into skills/) have no entry, which is how the API tells
// managed from unmanaged.
type Ledger struct {
	Version int                       `json:"version"`
	Skills  map[string]*InstallRecord `json:"skills"`
	Updated time.Time                 `json:"updated"`
}
