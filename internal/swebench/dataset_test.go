package swebench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadInstancesJSONLAndPromptRedactsGoldFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dataset.jsonl")
	data := `{"instance_id":"repo__issue-1","repo":"owner/repo","base_commit":"abc123","problem_statement":"Fix the failing parser.","hints_text":"Look at parser.go.","patch":"SECRET_GOLD_PATCH","test_patch":"SECRET_TEST_PATCH","FAIL_TO_PASS":["tests/test_parser.py::test_bug"],"PASS_TO_PASS":["tests/test_parser.py::test_existing"]}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write dataset: %v", err)
	}

	instances, err := LoadInstances(path, 0)
	if err != nil {
		t.Fatalf("LoadInstances: %v", err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	inst := instances[0]
	if inst.Patch != "SECRET_GOLD_PATCH" || inst.TestPatch != "SECRET_TEST_PATCH" {
		t.Fatalf("expected gold fields to decode for reporting")
	}

	prompt := inst.BuildPrompt()
	for _, forbidden := range []string{
		"SECRET_GOLD_PATCH",
		"SECRET_TEST_PATCH",
		"tests/test_parser.py::test_bug",
		"tests/test_parser.py::test_existing",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt leaked forbidden gold value %q:\n%s", forbidden, prompt)
		}
	}
	if !strings.Contains(prompt, "Fix the failing parser.") {
		t.Fatalf("prompt missing problem statement: %s", prompt)
	}
}

func TestLoadInstancesJSONLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dataset.json")
	data := `[
		{"instance_id":"one","repo":"owner/repo","base_commit":"abc","problem_statement":"first"},
		{"instance_id":"two","repo":"owner/repo","base_commit":"def","problem_statement":"second"}
	]`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write dataset: %v", err)
	}

	instances, err := LoadInstances(path, 1)
	if err != nil {
		t.Fatalf("LoadInstances: %v", err)
	}
	if len(instances) != 1 || instances[0].InstanceID != "one" {
		t.Fatalf("unexpected limited instances: %#v", instances)
	}
}

func TestLoadInstancesAcceptsStringEncodedTestLists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dataset.jsonl")
	data := `{"instance_id":"repo__issue-2","repo":"owner/repo","base_commit":"abc123","problem_statement":"Fix it.","FAIL_TO_PASS":"[\"tests/test_bug.py::test_one\"]","PASS_TO_PASS":"tests/test_ok.py::test_existing"}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write dataset: %v", err)
	}

	instances, err := LoadInstances(path, 0)
	if err != nil {
		t.Fatalf("LoadInstances: %v", err)
	}
	if got := []string(instances[0].FailToPass); len(got) != 1 || got[0] != "tests/test_bug.py::test_one" {
		t.Fatalf("unexpected FAIL_TO_PASS: %#v", got)
	}
	if got := []string(instances[0].PassToPass); len(got) != 1 || got[0] != "tests/test_ok.py::test_existing" {
		t.Fatalf("unexpected PASS_TO_PASS: %#v", got)
	}
}

func TestSafeID(t *testing.T) {
	if got := SafeID("django/django issue#1"); got != "django-django-issue-1" {
		t.Fatalf("SafeID() = %q", got)
	}
}
