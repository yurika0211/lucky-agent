// Package intentclassify is a pure this-turn classifier.
//
// It consumes only the current user text and returns multi-label hits plus the
// rule that fired. It does not choose a budget, tool allowlist, or skill name.
// Callers map labels to their own side effects:
//
//   - request_profile: budget only
//   - execpolicy.AllowedTools / agent tool gating: DisabledTools only
//   - skillroute: hints only
//
// Model classification is not a path of this package. Tidal retrieval tags and
// cron intent_key stay out.
package intentclassify

import (
	"strings"
	"unicode"
)

// Label is one intent dimension. A turn may carry several labels.
type Label string

const (
	LabelChat     Label = "chat"
	LabelCode     Label = "code"
	LabelTool     Label = "tool"
	LabelWeb      Label = "web"
	LabelMemory   Label = "memory"
	LabelSkill    Label = "skill"
	LabelComputer Label = "computer"
	LabelNegation Label = "negation"

	LabelNoTools          Label = "no_tools"
	LabelExplain          Label = "explain"
	LabelLocal            Label = "local"
	LabelEdit             Label = "edit"
	LabelToolSystem       Label = "tool_system"
	LabelTime             Label = "time"
	LabelCalc             Label = "calc"
	LabelMemoryHygiene    Label = "memory_hygiene"
	LabelRemember         Label = "remember"
	LabelRAGIndex         Label = "rag_index"
	LabelMedia            Label = "media"
	LabelDatabase         Label = "database"
	LabelDelegate         Label = "delegate"
	LabelContinuation     Label = "continuation"
	LabelLuckyAgentMemory Label = "luckyagent_memory"
	LabelNoExecute        Label = "no_execute"
	LabelNeedsTerminal    Label = "needs_terminal"
	LabelReadOnlySource   Label = "read_only_source"
	LabelReadOnlyMutation Label = "read_only_mutation"
	LabelNegatedDelete    Label = "negated_delete"
	LabelNoMemoryWrite    Label = "no_memory_write"
	LabelNoRAGIndex       Label = "no_rag_index"
	LabelNoSkillRun       Label = "no_skill_run"
	LabelDocument         Label = "document"
	LabelDataFile         Label = "data_file"
	LabelOpenCLI          Label = "opencli"
	LabelTwitter          Label = "twitter"
	LabelWebFetch         Label = "web_fetch"
	LabelWebSearch        Label = "web_search"
	LabelHTTP             Label = "http"
	LabelCron             Label = "cron"
	LabelAutonomy         Label = "autonomy"
	LabelHeartbeat        Label = "heartbeat"
	LabelSubagent         Label = "subagent"
	LabelImage            Label = "image"
	LabelTTS              Label = "tts"
	LabelAutonomyListOnly Label = "autonomy_list_only"
)

// Rule names recorded on Hit.Rule so logs and tests can name the detector.
const (
	RuleProfileCode      = "profile_code"
	RuleProfileTool      = "profile_tool"
	RuleExplicitChat     = "explicit_chat"
	RuleContinuation     = "continuation"
	RuleNoTools          = "no_tools"
	RuleExplain          = "explain"
	RuleLocal            = "local"
	RuleEdit             = "edit"
	RuleToolSystem       = "tool_system"
	RuleWeb              = "web"
	RuleTime             = "time"
	RuleCalc             = "calc"
	RuleMemory           = "memory"
	RuleMemoryHygiene    = "memory_hygiene"
	RuleRemember         = "remember"
	RuleRAGIndex         = "rag_index"
	RuleSkill            = "skill"
	RuleMedia            = "media"
	RuleDatabase         = "database"
	RuleDelegate         = "delegate"
	RuleComputer         = "computer"
	RuleNoExecute        = "no_execute"
	RuleNeedsTerminal    = "needs_terminal"
	RuleReadOnlySource   = "read_only_source"
	RuleReadOnlyMutation = "read_only_mutation"
	RuleNegatedDelete    = "negated_delete"
	RuleNoMemoryWrite    = "no_memory_write"
	RuleNoRAGIndex       = "no_rag_index"
	RuleNoSkillRun       = "no_skill_run"
	RuleDocument         = "document"
	RuleDataFile         = "data_file"
	RuleOpenCLI          = "opencli"
	RuleTwitter          = "twitter"
	RuleWebFetch         = "web_fetch"
	RuleWebSearch        = "web_search"
	RuleHTTP             = "http"
	RuleCron             = "cron"
	RuleAutonomy         = "autonomy"
	RuleHeartbeat        = "heartbeat"
	RuleSubagent         = "subagent"
	RuleImage            = "image"
	RuleTTS              = "tts"
	RuleLuckyAgentMemory = "luckyagent_memory"
	RuleAutonomyListOnly = "autonomy_list_only"
)

// Hit is one fired rule. Term is the vocabulary entry that matched, if any.
type Hit struct {
	Label Label
	Rule  string
	Term  string
}

// Result is the classifier output for one turn. It never includes a budget,
// tool list, or skill name.
type Result struct {
	Text string
	Hits []Hit
}

// Classify inspects this-turn text only.
func Classify(input string) Result {
	text := strings.ToLower(strings.TrimSpace(input))
	r := Result{Text: text}
	if text == "" {
		return r
	}
	intentText := stripNoToolPhrases(text)

	if term, ok := matchExactPhrase(stripEdgePunct(text), explicitChatPhrases); ok {
		r.add(LabelChat, RuleExplicitChat, term)
	}
	if term, ok := matchContinuation(text); ok {
		r.add(LabelContinuation, RuleContinuation, term)
	}
	if term, ok := matchAny(text, profileCodeTerms); ok {
		r.add(LabelCode, RuleProfileCode, term)
	}
	if term, ok := matchAny(text, profileToolTerms); ok {
		r.add(LabelTool, RuleProfileTool, term)
	}

	if term, ok := matchAny(text, noToolTerms); ok {
		r.add(LabelNoTools, RuleNoTools, term)
		r.add(LabelNegation, RuleNoTools, term)
	}
	if term, ok := matchAny(intentText, explainTerms); ok {
		r.add(LabelExplain, RuleExplain, term)
		r.add(LabelNegation, RuleExplain, term)
		if !r.Has(LabelChat) {
			r.add(LabelChat, RuleExplain, term)
		}
	}

	if term, ok := detectToolSystem(intentText); ok {
		r.add(LabelToolSystem, RuleToolSystem, term)
		r.add(LabelTool, RuleToolSystem, term)
	}
	if term, ok := detectLocal(intentText); ok {
		r.add(LabelLocal, RuleLocal, term)
		r.add(LabelTool, RuleLocal, term)
	}
	if term, ok := matchAny(intentText, editTerms); ok {
		r.add(LabelEdit, RuleEdit, term)
		r.add(LabelCode, RuleEdit, term)
	}
	if term, ok := matchAny(intentText, webTerms); ok {
		r.add(LabelWeb, RuleWeb, term)
	}
	if term, ok := matchAny(intentText, timeTerms); ok {
		r.add(LabelTime, RuleTime, term)
		r.add(LabelTool, RuleTime, term)
	}
	if term, ok := matchAny(intentText, calcTerms); ok {
		r.add(LabelCalc, RuleCalc, term)
		r.add(LabelTool, RuleCalc, term)
	}
	if term, ok := matchAny(intentText, memoryTerms); ok {
		r.add(LabelMemory, RuleMemory, term)
	}
	if term, ok := matchAny(intentText, memoryHygieneTerms); ok {
		r.add(LabelMemoryHygiene, RuleMemoryHygiene, term)
		r.add(LabelMemory, RuleMemoryHygiene, term)
	}
	if term, ok := matchAny(intentText, rememberTerms); ok {
		r.add(LabelRemember, RuleRemember, term)
		r.add(LabelMemory, RuleRemember, term)
	}
	if term, ok := matchAny(intentText, ragIndexTerms); ok {
		r.add(LabelRAGIndex, RuleRAGIndex, term)
		r.add(LabelMemory, RuleRAGIndex, term)
	}
	if term, ok := matchAny(intentText, skillTerms); ok {
		r.add(LabelSkill, RuleSkill, term)
	}
	if term, ok := detectMedia(intentText); ok && !r.Has(LabelToolSystem) {
		r.add(LabelMedia, RuleMedia, term)
	}
	if term, ok := matchAny(intentText, databaseTerms); ok {
		r.add(LabelDatabase, RuleDatabase, term)
		r.add(LabelTool, RuleDatabase, term)
	}
	if term, ok := matchAny(intentText, delegateTerms); ok {
		r.add(LabelDelegate, RuleDelegate, term)
		r.add(LabelTool, RuleDelegate, term)
	}
	if term, ok := matchAny(intentText, computerTerms); ok {
		r.add(LabelComputer, RuleComputer, term)
	}

	if term, ok := matchAny(intentText, noExecuteTerms); ok {
		r.add(LabelNoExecute, RuleNoExecute, term)
		r.add(LabelNegation, RuleNoExecute, term)
	}
	if term, ok := matchAny(intentText, readOnlySourceTerms); ok {
		r.add(LabelReadOnlySource, RuleReadOnlySource, term)
		r.add(LabelNegation, RuleReadOnlySource, term)
	}
	if term, ok := matchAny(intentText, readOnlyMutationTerms); ok {
		r.add(LabelReadOnlyMutation, RuleReadOnlyMutation, term)
		r.add(LabelNegation, RuleReadOnlyMutation, term)
	}
	if term, ok := matchAny(intentText, negatedDeleteTerms); ok {
		r.add(LabelNegatedDelete, RuleNegatedDelete, term)
		r.add(LabelNegation, RuleNegatedDelete, term)
	}
	if term, ok := matchAny(intentText, noMemoryWriteTerms); ok {
		r.add(LabelNoMemoryWrite, RuleNoMemoryWrite, term)
		r.add(LabelNegation, RuleNoMemoryWrite, term)
	}
	if term, ok := matchAny(intentText, noRAGIndexTerms); ok {
		r.add(LabelNoRAGIndex, RuleNoRAGIndex, term)
		r.add(LabelNegation, RuleNoRAGIndex, term)
	}
	if term, ok := matchAny(intentText, noSkillRunTerms); ok {
		r.add(LabelNoSkillRun, RuleNoSkillRun, term)
		r.add(LabelNegation, RuleNoSkillRun, term)
	}
	if term, ok := matchAny(intentText, documentTerms); ok {
		r.add(LabelDocument, RuleDocument, term)
	}
	if term, ok := matchAny(intentText, dataFileTerms); ok {
		r.add(LabelDataFile, RuleDataFile, term)
	}
	if term, ok := matchAny(intentText, autonomyListOnlyTerms); ok {
		r.add(LabelAutonomyListOnly, RuleAutonomyListOnly, term)
	}

	if !r.Has(LabelReadOnlySource) {
		if term, ok := matchAny(intentText, needsTerminalTerms); ok {
			if _, blocked := matchAny(intentText, noTerminalCommandTerms); !blocked {
				r.add(LabelNeedsTerminal, RuleNeedsTerminal, term)
			}
		}
	}

	if r.Has(LabelWeb) {
		if term, ok := matchAny(intentText, openCLITerms); ok {
			r.add(LabelOpenCLI, RuleOpenCLI, term)
		}
		if term, ok := matchAny(intentText, twitterTerms); ok {
			r.add(LabelTwitter, RuleTwitter, term)
			if !r.Has(LabelOpenCLI) {
				r.add(LabelOpenCLI, RuleTwitter, term)
			}
		}
		if term, ok := matchAny(intentText, webFetchTerms); ok {
			r.add(LabelWebFetch, RuleWebFetch, term)
		}
		if term, ok := matchAny(intentText, webSearchTerms); ok {
			r.add(LabelWebSearch, RuleWebSearch, term)
		}
		if term, ok := matchAny(intentText, httpTerms); ok {
			if _, blocked := matchAny(intentText, noHTTPTerms); !blocked {
				r.add(LabelHTTP, RuleHTTP, term)
			}
		}
	}
	if r.Has(LabelDelegate) {
		if term, ok := matchAny(intentText, cronTerms); ok {
			r.add(LabelCron, RuleCron, term)
		}
		if term, ok := matchAny(intentText, autonomyTerms); ok {
			r.add(LabelAutonomy, RuleAutonomy, term)
		}
		if term, ok := matchAny(intentText, heartbeatTerms); ok {
			r.add(LabelHeartbeat, RuleHeartbeat, term)
		}
		if term, ok := matchAny(intentText, subagentTerms); ok {
			r.add(LabelSubagent, RuleSubagent, term)
		}
	}
	if r.Has(LabelMedia) {
		if term, ok := matchAny(intentText, imageTerms); ok {
			r.add(LabelImage, RuleImage, term)
		}
		if term, ok := matchAny(intentText, ttsTerms); ok {
			r.add(LabelTTS, RuleTTS, term)
		}
	}
	if detectLuckyAgentMemory(text) {
		r.add(LabelLuckyAgentMemory, RuleLuckyAgentMemory, "memory+backend")
	}
	return r
}

func (r *Result) add(label Label, rule, term string) {
	r.Hits = append(r.Hits, Hit{Label: label, Rule: rule, Term: term})
}

// Empty reports a blank turn.
func (r Result) Empty() bool {
	return strings.TrimSpace(r.Text) == ""
}

// Has reports whether any hit carries label.
func (r Result) Has(label Label) bool {
	for _, hit := range r.Hits {
		if hit.Label == label {
			return true
		}
	}
	return false
}

// HasAny reports whether any of the labels fired.
func (r Result) HasAny(labels ...Label) bool {
	for _, label := range labels {
		if r.Has(label) {
			return true
		}
	}
	return false
}

// HasRule reports whether a named detector fired.
func (r Result) HasRule(rule string) bool {
	for _, hit := range r.Hits {
		if hit.Rule == rule {
			return true
		}
	}
	return false
}

// StrongTool is the AllowedTools "recognized tool intent" predicate.
func (r Result) StrongTool() bool {
	return r.Has(LabelLocal) || r.Has(LabelToolSystem) || r.Has(LabelEdit) ||
		r.Has(LabelWeb) || r.Has(LabelTime) || r.Has(LabelCalc) ||
		r.Has(LabelMemory) || r.Has(LabelMemoryHygiene) || r.Has(LabelRemember) ||
		r.Has(LabelRAGIndex) || r.Has(LabelSkill) || r.Has(LabelMedia) ||
		r.Has(LabelDatabase) || r.Has(LabelDelegate) || r.Has(LabelComputer)
}

// Recognized is true when AllowedTools should replace the current tool list.
// Empty text is unrecognized so the caller keeps its tools.
func (r Result) Recognized() bool {
	if r.Empty() {
		return false
	}
	if r.Has(LabelNoTools) && !r.StrongTool() {
		return true
	}
	if r.Has(LabelExplain) && !r.StrongTool() {
		return true
	}
	return r.StrongTool()
}

// ContainsAny is the shared substring matcher. Needles are compared in lower case.
func ContainsAny(text string, needles ...string) bool {
	_, ok := matchAny(text, needles)
	return ok
}

func matchAny(text string, needles []string) (string, bool) {
	for _, needle := range needles {
		n := strings.ToLower(strings.TrimSpace(needle))
		if n != "" && strings.Contains(text, n) {
			return needle, true
		}
	}
	return "", false
}

func matchExactPhrase(text string, phrases []string) (string, bool) {
	for _, phrase := range phrases {
		if text == phrase {
			return phrase, true
		}
	}
	return "", false
}

func stripEdgePunct(text string) string {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimFunc(trimmed, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
	return strings.ToLower(strings.TrimSpace(trimmed))
}

func matchContinuation(text string) (string, bool) {
	trimmed := stripEdgePunct(text)
	if term, ok := matchExactPhrase(trimmed, continuationPhrases); ok {
		return term, true
	}
	runes := []rune(trimmed)
	if len(runes) == 0 || len(runes) > 24 {
		return "", false
	}
	if term, ok := matchAny(trimmed, continuationTokens); ok {
		return term, true
	}
	return "", false
}

func stripNoToolPhrases(text string) string {
	out := text
	for _, phrase := range noToolTerms {
		out = strings.ReplaceAll(out, phrase, " ")
	}
	return out
}

func detectToolSystem(text string) (string, bool) {
	if term, ok := matchAny(text, toolSystemTerms); ok {
		return term, true
	}
	if _, ok := matchAny(text, toolSystemOnlyTerms); !ok {
		return "", false
	}
	img, ok := matchAny(text, toolSystemImageTerms)
	if !ok {
		return "", false
	}
	if _, ok := matchAny(text, toolSystemAgentTerms); !ok {
		return "", false
	}
	return img, true
}

func detectLocal(text string) (string, bool) {
	if term, ok := matchAny(text, localTerms); ok {
		return term, true
	}
	if _, ok := matchAny(text, localLookTerms); !ok {
		return "", false
	}
	return matchAny(text, localLookTargets)
}

func detectMedia(text string) (string, bool) {
	if _, skip := matchAny(text, mediaSkipTerms); skip {
		return "", false
	}
	return matchAny(text, mediaTerms)
}

func detectLuckyAgentMemory(text string) bool {
	if _, ok := matchAny(text, luckyAgentMemoryTerms); !ok {
		return false
	}
	_, ok := matchAny(text, luckyAgentBackendTerms)
	return ok
}

// WantsDelete is true when the turn names a delete action that is not negated.
func (r Result) WantsDelete() bool {
	if r.Has(LabelNegatedDelete) || r.Has(LabelNoExecute) {
		return false
	}
	_, ok := matchAny(r.Text, deleteTerms)
	return ok
}
