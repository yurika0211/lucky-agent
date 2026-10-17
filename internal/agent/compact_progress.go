package agent

import (
	"fmt"
	"strings"

	compactlib "github.com/yurika0211/luckyagent-tools/compact"
)

// CompactProgress is a client-visible progress update for session compaction.
// It is carried on ChatEventCompact and mapped to SSE/WebSocket "compact" events.
type CompactProgress = compactlib.Progress

// CompactDisplay builds a short UI-facing title/subtitle for compact results.
func CompactDisplay(result *CompactSessionResult) map[string]string {
	if result == nil {
		return map[string]string{}
	}
	title := "Context compressed"
	switch {
	case result.DryRun:
		title = "Compact dry-run"
	case strings.HasPrefix(result.Trigger, "auto-degraded"), result.SummarySource == "local", result.SummarySource == "local-mapreduce":
		title = "Context compressed (local)"
	case strings.Contains(result.SummarySource, "mapreduce"):
		title = "Context compressed (map-reduce)"
	case result.Trigger == "auto":
		title = "Context auto-compressed"
	}
	subtitle := fmt.Sprintf("%d → %d tokens", result.PreTokenEstimate, result.PostTokenEstimate)
	if result.DroppedMessages > 0 {
		subtitle = fmt.Sprintf("%s · dropped %d msgs", subtitle, result.DroppedMessages)
	}
	if src := strings.TrimSpace(result.SummarySource); src != "" {
		subtitle = fmt.Sprintf("%s · %s", subtitle, src)
	}
	return map[string]string{
		"title":    title,
		"subtitle": subtitle,
		"message":  title + " · " + subtitle,
	}
}

func compactProgressFromResult(phase, message string, result *CompactSessionResult) CompactProgress {
	p := CompactProgress{Phase: phase, Message: message}
	if result == nil {
		return p
	}
	p.Trigger = result.Trigger
	p.BoundaryID = result.BoundaryID
	p.SummarySource = result.SummarySource
	p.PreTokenEstimate = result.PreTokenEstimate
	p.PostTokenEstimate = result.PostTokenEstimate
	p.DroppedMessages = result.DroppedMessages
	p.RetainedMessages = result.RetainedMessages
	p.RestoredAttachments = result.RestoredAttachments
	if message == "" {
		if d := CompactDisplay(result); d["message"] != "" {
			p.Message = d["message"]
		}
	}
	return p
}

func emitCompactProgress(opts CompactSessionOptions, progress CompactProgress) {
	if opts.OnProgress == nil {
		return
	}
	if strings.TrimSpace(progress.Message) == "" {
		switch progress.Phase {
		case "start":
			progress.Message = "Compressing conversation context…"
		case "progress":
			if progress.Chunks > 0 && progress.Chunk > 0 {
				progress.Message = fmt.Sprintf("Compressing context · chunk %d/%d", progress.Chunk, progress.Chunks)
			} else {
				progress.Message = "Compressing conversation context…"
			}
		case "done":
			progress.Message = "Context compressed"
		case "degraded":
			progress.Message = "Context compressed with local fallback"
		case "failed":
			progress.Message = "Context compression failed"
		}
	}
	opts.OnProgress(progress)
}

func emitCompactChatEvent(emit func(ChatEvent), progress CompactProgress) {
	if emit == nil {
		return
	}
	cp := progress
	emit(ChatEvent{
		Type:    ChatEventCompact,
		Name:    progress.Phase,
		Content: progress.Message,
		Compact: &cp,
	})
}
