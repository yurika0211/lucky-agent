package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TrialOptions configures the isolated load of a staged skill.
type TrialOptions struct {
	// ProbeCLI enables the `<script> --help` probe. It executes the staged
	// skill's code, so callers must only set it after the static scan is clean.
	ProbeCLI     bool
	ProbeTimeout time.Duration
	TotalBudget  time.Duration
}

// TrialResult is what a user reviews before confirming.
type TrialResult struct {
	Info       *SkillInfo
	Capability *CapabilitySummary
}

// TrialLoad parses a staged skill in isolation and reports what it would
// contribute. stagingRoot is the *parent* of the skill directory: SkillLoader
// iterates its directory and descends one level, so pointing it at the skill
// itself would find nothing.
//
// Isolation comes from a throwaway tool.Registry — the live registry is never
// touched, so a partial or failed trial leaves nothing behind to clean up.
func TrialLoad(stagingRoot string, opts TrialOptions) (*TrialResult, error) {
	// Pass 1 is parse-only: pure file I/O, nothing executes. It yields the skill
	// name and declared tools, which is everything the collision precheck needs.
	loader := NewSkillLoader(stagingRoot).WithCLIInspect(false, 0)
	throwaway := NewRegistry()
	sr := NewSkillRegistry(throwaway, loader)

	metas, err := sr.Discover()
	if err != nil {
		return nil, fmt.Errorf("trial discover: %w", err)
	}
	if len(metas) != 1 {
		return nil, fmt.Errorf("expected exactly one skill in the staged tree, found %d", len(metas))
	}
	name := metas[0].Name
	if err := sr.Load(name); err != nil {
		return nil, fmt.Errorf("trial load: %w", err)
	}
	if err := sr.Validate(name); err != nil {
		return nil, fmt.Errorf("trial validate: %w", err)
	}

	infos := sr.SkillInfos()
	if len(infos) != 1 {
		return nil, fmt.Errorf("trial load produced %d skills", len(infos))
	}
	info := infos[0]

	summary := &CapabilitySummary{
		Name:        info.Name,
		Aliases:     info.Aliases,
		Description: info.Description,
		Summary:     info.Summary,
		Scripts:     listScripts(info.Dir),
		Tools:       capabilityTools(info, "declared"),
	}

	// Pass 2 runs the probe. It is additive: on failure the summary keeps the
	// parse-only view rather than the install failing.
	if opts.ProbeCLI {
		probed, probeErrs := probeCapabilities(stagingRoot, info.Name, opts)
		summary.Probed = probed != nil
		summary.ProbeErrors = probeErrs
		if probed != nil {
			summary.Tools = capabilityTools(probed, "cli_probe")
			info = probed
		}
	}

	return &TrialResult{Info: info, Capability: summary}, nil
}

// probeCapabilities re-parses with the `--help` probe on, under a scrubbed
// environment and a wall-clock budget.
func probeCapabilities(stagingRoot, name string, opts TrialOptions) (*SkillInfo, []string) {
	// The probe's HOME/TMPDIR must live outside stagingRoot: SkillLoader treats
	// every child directory of stagingRoot as a skill, so a scratch directory
	// created in there would be discovered as a second skill.
	probeHome, err := os.MkdirTemp("", "lh-skill-probe-")
	if err != nil {
		return nil, []string{fmt.Sprintf("probe sandbox: %v", err)}
	}
	defer os.RemoveAll(probeHome)

	loader := NewSkillLoader(stagingRoot).
		WithCLIInspect(true, opts.ProbeTimeout).
		WithProbeEnv(minimalProbeEnv(probeHome))

	type outcome struct {
		info *SkillInfo
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		sr := NewSkillRegistry(NewRegistry(), loader)
		if _, err := sr.Discover(); err != nil {
			done <- outcome{nil, err}
			return
		}
		if err := sr.Load(name); err != nil {
			done <- outcome{nil, err}
			return
		}
		infos := sr.SkillInfos()
		if len(infos) != 1 {
			done <- outcome{nil, fmt.Errorf("probe produced %d skills", len(infos))}
			return
		}
		done <- outcome{infos[0], nil}
	}()

	budget := opts.TotalBudget
	if budget <= 0 {
		budget = 90 * time.Second
	}
	select {
	case res := <-done:
		if res.err != nil {
			return nil, []string{res.err.Error()}
		}
		return res.info, nil
	case <-time.After(budget):
		// The per-probe deadline inside SkillLoader still applies, so the
		// goroutine unwinds on its own; we just stop waiting for it.
		return nil, []string{fmt.Sprintf("probe exceeded the %s budget", budget)}
	}
}

// minimalProbeEnv is the environment handed to unvetted skill code. It carries
// no credentials: the server's own environment holds provider API keys, and a
// hostile `--help` would otherwise be able to read and exfiltrate them.
//
// probeHome must be a scratch directory outside the staged tree.
func minimalProbeEnv(probeHome string) []string {
	tmp := filepath.Join(probeHome, "tmp")
	_ = os.MkdirAll(tmp, 0o700)
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + probeHome,
		"TMPDIR=" + tmp,
		"LC_ALL=C",
	}
	if lang := os.Getenv("LANG"); lang != "" {
		env = append(env, "LANG="+lang)
	}
	return env
}

func capabilityTools(info *SkillInfo, origin string) []CapabilityTool {
	out := make([]CapabilityTool, 0, len(info.Tools))
	for _, td := range info.Tools {
		o := origin
		if len(td.Command) > 0 {
			o = "cli_probe"
		}
		out = append(out, CapabilityTool{
			Name:          td.Name,
			Description:   td.Description,
			ExposeToModel: td.ExposeToModel,
			Command:       td.Command,
			Origin:        o,
			FullName:      fmt.Sprintf("skill_%s_%s", info.Name, td.Name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func listScripts(skillDir string) []string {
	entries, err := os.ReadDir(filepath.Join(skillDir, "scripts"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, "scripts/"+e.Name())
	}
	sort.Strings(out)
	return out
}

// CheckCollisions reports why a staged skill cannot coexist with what is already
// installed. Names are compared post-sanitizeName because that is the key the
// registry actually uses, and it is many-to-one ("a b" and "a-b" both collapse).
func CheckCollisions(summary *CapabilitySummary, existing *SkillRegistry, tools *Registry) []string {
	var out []string
	name := sanitizeName(summary.Name)

	if name == "" {
		return []string{"skill name is empty after normalization"}
	}
	if _, reserved := reservedSkillNames[name]; reserved {
		out = append(out, fmt.Sprintf("%q is a reserved name", name))
	}

	if existing != nil {
		for _, meta := range existing.List() {
			if sanitizeName(meta.Name) == name {
				out = append(out, fmt.Sprintf("a skill named %q is already installed", meta.Name))
				break
			}
		}
	}

	if tools != nil {
		for _, ct := range summary.Tools {
			if existingTool, ok := tools.Get(ct.FullName); ok && existingTool.Source != summary.Name {
				out = append(out, fmt.Sprintf("tool %q is already registered by %q", ct.FullName, existingTool.Source))
			}
		}
	}

	sort.Strings(out)
	return out
}

// ValidSkillName reports whether a name is safe to use as a path segment and as
// a URL segment. sanitizeName permits CJK, so this cannot be a plain ASCII
// allowlist — several installed skills have Chinese names.
func ValidSkillName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	if name != sanitizeName(name) {
		return false
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return false
	}
	return true
}
