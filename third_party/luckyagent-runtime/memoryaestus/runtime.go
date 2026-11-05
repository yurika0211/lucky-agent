package memoryaestus

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yurika0211/luckyagent-tools/memory"
)

type Options struct {
	HomeDir                                string
	ShortTermMaxTurns, MidTermMaxSummaries int
	Tidal                                  TidalOptions
}

type TidalOptions struct {
	Enabled                      bool
	StorePath                    string
	Beta, MaxBoost, LearningRate float64
	MinSamples                   int
}

// TidalStorePath is shared by runtime assembly and offline inspection commands.
func TidalStorePath(home, configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return filepath.Join(home, "runtime", "tidal_memory.db")
	}
	if filepath.IsAbs(configured) {
		return configured
	}
	return filepath.Join(home, configured)
}

// Open owns backend construction. Callers own the returned Store's Close;
// partial initialization failures release the resources created here.
func Open(opts Options) (memory.Runtime, error) {
	if strings.TrimSpace(opts.HomeDir) == "" {
		return memory.Runtime{}, fmt.Errorf("memory home is required")
	}
	store, err := NewStore(filepath.Join(opts.HomeDir, "memory"))
	if err != nil {
		return memory.Runtime{}, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = store.Close()
		}
	}()
	if opts.Tidal.Enabled {
		db, err := OpenTidalStore(TidalStorePath(opts.HomeDir, opts.Tidal.StorePath))
		if err != nil {
			return memory.Runtime{}, fmt.Errorf("open tidal: %w", err)
		}
		cfg := DefaultTidalRerankerConfig()
		cfg.Beta = opts.Tidal.Beta
		cfg.MaxBoost = opts.Tidal.MaxBoost
		cfg.LearningRate = opts.Tidal.LearningRate
		cfg.MinSamples = opts.Tidal.MinSamples
		reranker, err := NewPersistentTidalMemoryReranker(cfg, db)
		if err != nil {
			_ = db.Close()
			return memory.Runtime{}, err
		}
		store.SetActivationReranker(reranker)
	}
	if opts.ShortTermMaxTurns <= 0 {
		opts.ShortTermMaxTurns = 10
	}
	if opts.MidTermMaxSummaries <= 0 {
		opts.MidTermMaxSummaries = 100
	}
	mid, err := NewMidTermStore(filepath.Join(opts.HomeDir, "memory", "30_Sessions"), opts.MidTermMaxSummaries)
	if err != nil {
		return memory.Runtime{}, err
	}
	complete = true
	return memory.Runtime{Store: store, ShortTerm: NewSessionShortTermStore(opts.ShortTermMaxTurns), Summaries: mid}, nil
}
