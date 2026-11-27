package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
)

func repairFixtureMessages() []provider.Message {
	return []provider.Message{
		{Role: "user", Content: "Fix internal/agent/context_planner.go and keep the failing command."},
		{Role: "tool", Name: "terminal", Content: "go test ./internal/agent failed: context planner dropped latest user turn"},
	}
}

func repairSectionSummary(completedBullets int) string {
	var b strings.Builder
	b.WriteString("Current user goal:\n")
	b.WriteString("Keep internal/agent/context_planner.go and the failing command available after compact.\n")
	b.WriteString("Completed work:\n")
	for i := 0; i < completedBullets; i++ {
		b.WriteString("- Recorded compact work item ")
		b.WriteString(strings.Repeat("x", 80))
		b.WriteString(".\n")
	}
	b.WriteString("Pending work:\n")
	b.WriteString("Add restore attachments and trace output before considering auto compact.\n")
	b.WriteString("Key files and functions:\n")
	b.WriteString("internal/agent/context_planner.go\n")
	b.WriteString("Commands and test results:\n")
	b.WriteString("go test ./internal/agent failed: context planner dropped latest user turn\n")
	b.WriteString("User constraints:\n")
	b.WriteString("Do not weaken the identifier check.\n")
	b.WriteString("Uncertain facts:\n")
	b.WriteString("None.\n")
	return b.String()
}

func TestTrimCompactSummaryDropsCompletedWorkBeforeGoal(t *testing.T) {
	summary := repairSectionSummary(200)
	if len([]rune(summary)) <= compactlib.MaxSummaryRunes {
		t.Fatalf("fixture is not over the cap: %d", len([]rune(summary)))
	}
	trimmed := trimCompactSummary(summary, compactlib.MaxSummaryRunes)
	if got := len([]rune(trimmed)); got > compactlib.MaxSummaryRunes {
		t.Fatalf("trimmed length = %d, cap = %d", got, compactlib.MaxSummaryRunes)
	}
	if !strings.Contains(trimmed, "Current user goal:") || !strings.Contains(trimmed, "Pending work:") {
		t.Fatalf("trim dropped a protected section:\n%s", trimmed)
	}
	if !strings.Contains(trimmed, "internal/agent/context_planner.go") {
		t.Fatalf("trim dropped the file section:\n%s", trimmed)
	}
	if !strings.Contains(trimmed, compactTrimMarker) {
		t.Fatal("trim did not mark the omitted completed-work tail")
	}
	if strings.Count(trimmed, "Recorded compact work item") >= 200 {
		t.Fatal("completed work was not shortened")
	}
}

func TestRepairCompactSummaryKeepsOverlongSummaryWithIdentifiers(t *testing.T) {
	messages := repairFixtureMessages()
	summary := repairSectionSummary(200)
	if validation := validateCompactSummary(summary, messages); validation.Valid {
		t.Fatal("over-long fixture unexpectedly passed validation")
	}
	repaired, ok := repairCompactSummary(summary, messages)
	if !ok {
		t.Fatal("over-long summary with every identifier was not repaired")
	}
	if validation := validateCompactSummary(repaired, messages); !validation.Valid {
		t.Fatalf("repaired summary still invalid: %s", validation.Reason)
	}
	if len([]rune(repaired)) > compactlib.MaxSummaryRunes {
		t.Fatalf("repaired length = %d", len([]rune(repaired)))
	}
	for _, id := range requiredCompactIdentifiers(messages) {
		if !strings.Contains(repaired, id) {
			t.Fatalf("repaired summary dropped identifier %q", id)
		}
	}
}

func TestRepairCompactSummaryAppendsMissingIdentifiers(t *testing.T) {
	messages := repairFixtureMessages()
	summary := repairSectionSummary(2)
	summary = strings.ReplaceAll(summary, "go test ./internal/agent failed: context planner dropped latest user turn", "the agent tests failed")
	missing := missingCompactIdentifiers(summary, requiredCompactIdentifiers(messages))
	if len(missing) == 0 {
		t.Fatal("fixture did not drop an identifier")
	}
	if validation := validateCompactSummary(summary, messages); validation.Valid {
		t.Fatal("identifier-dropping fixture unexpectedly passed validation")
	}
	repaired, ok := repairCompactSummary(summary, messages)
	if !ok {
		t.Fatal("summary missing identifiers was not repaired")
	}
	if validation := validateCompactSummary(repaired, messages); !validation.Valid {
		t.Fatalf("repaired summary still invalid: %s", validation.Reason)
	}
	if !strings.Contains(repaired, "Source identifiers") {
		t.Fatalf("missing identifiers were not appended:\n%s", repaired)
	}
	for _, id := range missing {
		if !strings.Contains(repaired, id) {
			t.Fatalf("appendix lost %q", id)
		}
	}
}

func TestRepairCompactSummaryLeavesBlankSummaryRejected(t *testing.T) {
	messages := repairFixtureMessages()
	repaired, ok := repairCompactSummary("Looks good.", messages)
	if ok {
		t.Fatalf("blank summary was repaired into %q", repaired)
	}
	if repaired != "Looks good." {
		t.Fatalf("rejected summary changed: %q", repaired)
	}
}

func TestCompactSessionRepairsOverlongSummaryAndWritesBoundary(t *testing.T) {
	// The source has to stay inside the history budget. Otherwise both sides of
	// the benefit check are clipped to the same tail and a shorter summary
	// cannot win.
	body := "Fix internal/agent/context_planner.go and run go test ./internal/agent. " + strings.Repeat("planner-note ", 800)
	a, sess, prov := compactBenefitHarness(t, body)
	sess.AddToolMessage("terminal", "go test ./internal/agent passed after preserving latest user turn. "+strings.Repeat("pass-line ", 400))
	prov.summary = repairSectionSummary(200)
	before := sess.MessageCount()

	result, err := a.CompactSession(context.Background(), sess, "manual")
	if err != nil {
		t.Fatalf("over-long model summary was not repaired: %v", err)
	}
	if result.BoundaryID == "" || !strings.Contains(result.SummarySource, "repaired") {
		t.Fatalf("unexpected compact result: %+v", result)
	}
	if got := sess.MessageCount(); got <= before {
		t.Fatalf("repaired compact did not write a boundary: %d -> %d", before, got)
	}
	if len([]rune(result.Summary)) > compactlib.MaxSummaryRunes {
		t.Fatalf("stored summary length = %d", len([]rune(result.Summary)))
	}
	if !strings.Contains(result.Summary, "Current user goal:") || !strings.Contains(result.Summary, "Pending work:") {
		t.Fatalf("stored summary lost a protected section:\n%s", result.Summary)
	}
}

func TestCompactSessionRepairsMissingIdentifiers(t *testing.T) {
	body := "Fix internal/agent/context_planner.go and run go test ./internal/agent. " + strings.Repeat("planner-note ", 800)
	a, sess, prov := compactBenefitHarness(t, body)
	sess.AddToolMessage("terminal", "go test ./internal/agent failed: context planner dropped latest user turn. "+strings.Repeat("fail-line ", 400))
	summary := repairSectionSummary(2)
	summary = strings.ReplaceAll(summary, "go test ./internal/agent failed: context planner dropped latest user turn", "the agent tests failed")
	summary = strings.ReplaceAll(summary, "internal/agent/context_planner.go", "the context planner file")
	prov.summary = summary

	result, err := a.CompactSession(context.Background(), sess, "manual")
	if err != nil {
		t.Fatalf("summary missing identifiers was not repaired: %v", err)
	}
	if !strings.Contains(result.Summary, "Source identifiers") {
		t.Fatalf("stored summary has no identifier appendix:\n%s", result.Summary)
	}
	messages := sess.GetMessages()
	last := messages[len(messages)-1]
	if !session.IsCompactBoundary(last) {
		t.Fatal("repaired summary did not write a boundary")
	}
	for _, id := range requiredCompactIdentifiers(sess.GetMessages()[:len(messages)-1]) {
		if !strings.Contains(result.Summary, id) {
			t.Fatalf("stored summary dropped %q", id)
		}
	}
}

func TestCompactSessionRepairStillSkipsWhenNotSmaller(t *testing.T) {
	// The source is one short user turn. Repairing the model summary can make
	// it valid, but the candidate request must still shrink or the boundary
	// stays unwritten.
	a, sess, prov := compactBenefitHarness(t, "Fix internal/agent/context_planner.go.")
	sess.AddToolMessage("terminal", "go test ./internal/agent failed: context planner dropped latest user turn")
	prov.summary = repairSectionSummary(200)
	before := sess.MessageCount()

	_, err := a.CompactSession(context.Background(), sess, "manual")
	if !errors.Is(err, ErrCompactNoBenefit) {
		t.Fatalf("repaired summary that is not smaller = %v, want ErrCompactNoBenefit", err)
	}
	if got := sess.MessageCount(); got != before {
		t.Fatalf("no-benefit wrote a boundary: %d -> %d", before, got)
	}
}

func TestCompactSessionStillRejectsLowQualitySummary(t *testing.T) {
	sess := session.NewSession("compact-repair-blank", t.TempDir())
	sess.AddMessage("user", "Fix internal/agent/context_planner.go and run go test ./internal/agent.")
	sess.AddToolMessage("terminal", "go test ./internal/agent failed: context planner dropped latest user turn")
	before := sess.MessageCount()
	a := &Agent{
		provider:   &staticChatProvider{name: "static", content: "Looks good."},
		contextEst: contextx.NewTokenEstimator(4096),
	}
	_, err := a.CompactSession(context.Background(), sess, "manual")
	if err == nil || !strings.Contains(err.Error(), "invalid summary") {
		t.Fatalf("expected invalid summary error, got %v", err)
	}
	if got := sess.MessageCount(); got != before {
		t.Fatalf("rejected summary wrote a boundary: %d -> %d", before, got)
	}
}
