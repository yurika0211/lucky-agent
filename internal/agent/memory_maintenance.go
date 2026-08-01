package agent

import (
	"time"

	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/session"
)

// recordMemoryTurn moves turn accounting into the memory runtime.  Agent only
// consumes the immutable maintenance event; it no longer owns a shared
// mutable chat counter.
func (a *Agent) recordMemoryTurn(sess *session.Session) memory.MaintenanceEvent {
	if a == nil || a.memory == nil {
		return memory.MaintenanceEvent{}
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}
	return a.memory.RecordTurn(sessionID)
}

// applyMemoryMaintenance executes the actions selected by the memory
// coordinator after the turn has been persisted.  The scheduling decision is
// made under the coordinator lock, so concurrent turns cannot duplicate a
// cadence boundary even though the actual maintenance work may run afterward.
func (a *Agent) applyMemoryMaintenance(event memory.MaintenanceEvent) {
	if a == nil || a.memory == nil {
		return
	}
	if event.RunDecay {
		a.memory.Decay(0.05)
		a.memory.Expire()
	}
	if event.RunSummarize {
		a.autoSummarize()
	}
	if event.RunExpire && a.midTerm != nil {
		expireDays := 90
		if a.cfg != nil {
			if configured := a.cfg.Get().Memory.MidTermExpireDays; configured > 0 {
				expireDays = configured
			}
		}
		a.midTerm.ExpireOldSummaries(time.Duration(expireDays) * 24 * time.Hour)
	}
}
