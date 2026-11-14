package agent

import (
	"strings"

	"github.com/yurika0211/lucky-agent/agent/intentclassify"
)

// allowedToolsFromIntent maps classifier labels onto a turn-local allowlist.
// The classifier never returns tool names; this is the execpolicy side effect.
// Unrecognized turns return recognized=false so the caller keeps its tool list.
func allowedToolsFromIntent(classified intentclassify.Result, visibleSkillRunTools []string) (map[string]struct{}, bool) {
	allowed := map[string]struct{}{}
	if !classified.Recognized() {
		return allowed, false
	}
	if (classified.Has(intentclassify.LabelNoTools) || classified.Has(intentclassify.LabelExplain)) && !classified.StrongTool() {
		return allowed, true
	}

	localOrEdit := classified.Has(intentclassify.LabelLocal) || classified.Has(intentclassify.LabelToolSystem) || classified.Has(intentclassify.LabelEdit)
	negatedExecution := classified.Has(intentclassify.LabelNoExecute)
	readOnlySource := classified.Has(intentclassify.LabelReadOnlySource)
	readOnlyMutation := classified.Has(intentclassify.LabelReadOnlyMutation)
	needsTerminal := classified.Has(intentclassify.LabelNeedsTerminal)

	if localOrEdit {
		addIntentTools(allowed, "file_read", "file_list")
		if (!negatedExecution || needsTerminal) && !readOnlySource && !readOnlyMutation {
			addIntentTools(allowed, "terminal")
		}
		if classified.Has(intentclassify.LabelDataFile) {
			addIntentTools(allowed, "data")
		}
		if classified.Has(intentclassify.LabelDocument) {
			addIntentTools(allowed, "document_read")
		}
		if classified.Has(intentclassify.LabelToolSystem) {
			addIntentTools(allowed, "data")
		}
		if classified.Has(intentclassify.LabelEdit) && !readOnlySource && !readOnlyMutation {
			addIntentTools(allowed, "file_patch", "file_write", "file_mkdir", "file_move")
		}
		if classified.WantsDelete() {
			addIntentTools(allowed, "file_delete")
		}
	}
	if classified.Has(intentclassify.LabelComputer) {
		addIntentTools(allowed, "computer_observe", "computer_act")
	}
	if classified.Has(intentclassify.LabelWeb) {
		if classified.Has(intentclassify.LabelOpenCLI) {
			addIntentTools(allowed, "opencli")
		}
		if classified.Has(intentclassify.LabelWebFetch) {
			addIntentTools(allowed, "opencli", "web_fetch")
		}
		if classified.Has(intentclassify.LabelWebSearch) {
			addIntentTools(allowed, "web_search")
		}
		if !hasIntentTool(allowed, "opencli") && !hasIntentTool(allowed, "web_fetch") && !hasIntentTool(allowed, "web_search") {
			addIntentTools(allowed, "web_search", "web_fetch", "opencli")
		}
		if classified.Has(intentclassify.LabelHTTP) {
			addIntentTools(allowed, "http_request")
		}
	}
	if classified.Has(intentclassify.LabelTime) {
		addIntentTools(allowed, "current_time")
	}
	if classified.Has(intentclassify.LabelCalc) {
		addIntentTools(allowed, "calculate")
	}
	if classified.HasRule(intentclassify.RuleMemory) || classified.Has(intentclassify.LabelMemoryHygiene) ||
		(classified.Has(intentclassify.LabelRemember) && !classified.Has(intentclassify.LabelNoMemoryWrite)) {
		addIntentTools(allowed, "memory")
	}
	if classified.HasRule(intentclassify.RuleMemory) {
		addIntentTools(allowed, "rag_search")
	}
	if classified.Has(intentclassify.LabelRAGIndex) && !classified.Has(intentclassify.LabelNoRAGIndex) {
		addIntentTools(allowed, "rag_index")
	}
	if classified.Has(intentclassify.LabelSkill) {
		addIntentTools(allowed, "skill_read")
		if !classified.Has(intentclassify.LabelNoSkillRun) {
			addIntentTools(allowed, visibleSkillRunTools...)
		}
	}
	if classified.Has(intentclassify.LabelMedia) {
		if classified.Has(intentclassify.LabelImage) {
			addIntentTools(allowed, "image")
		}
		if classified.Has(intentclassify.LabelTTS) {
			addIntentTools(allowed, "text_to_speech")
		}
	}
	if classified.Has(intentclassify.LabelDatabase) {
		addIntentTools(allowed, "data")
	}
	if classified.Has(intentclassify.LabelDelegate) {
		if classified.Has(intentclassify.LabelCron) {
			addIntentTools(allowed, "cron")
		}
		if classified.Has(intentclassify.LabelAutonomy) {
			addIntentTools(allowed, "autonomy", "autonomy_status")
			if !classified.Has(intentclassify.LabelAutonomyListOnly) {
				addIntentTools(allowed, "autonomy_queue_add", "autonomy_worker_spawn")
			}
		}
		if classified.Has(intentclassify.LabelHeartbeat) {
			addIntentTools(allowed, "heartbeat")
		}
		if classified.Has(intentclassify.LabelSubagent) {
			addIntentTools(allowed, "subagent")
		}
	}
	return allowed, true
}

func addIntentTools(dst map[string]struct{}, names ...string) {
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			dst[name] = struct{}{}
		}
	}
}

func hasIntentTool(set map[string]struct{}, name string) bool {
	_, ok := set[name]
	return ok
}
