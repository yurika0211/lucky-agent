package tool

import (
	"strings"
	"testing"
	"time"
)

func TestAnnotateToolCallTemplates(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{"file_read", `{"path":"README.md","offset":1,"limit":20}`, "读取了 README.md 的前 20 行"},
		{"file_write", `{"path":"config.json","content":"abc"}`, "写入 config.json"},
		{"terminal", `{"command":"go test ./...","workdir":"/repo"}`, "在 /repo 执行 go test ./..."},
		{"web_search", `{"query":"Telegram HTML"}`, "搜索了「Telegram HTML」"},
		{"delegate_task", `{"description":"review queue handling"}`, "委托子 Agent：review queue handling"},
		{"sql_query", `{"query":"SELECT 1"}`, "执行了 SQL 查询：SELECT 1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := AnnotateToolCall(test.name, test.args, "ok"); !strings.Contains(got, test.want) {
				t.Fatalf("AnnotateToolCall() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNewTraceRecordMarksErrorsAndDuration(t *testing.T) {
	record := NewTraceRecord("file_read", `{"path":"secret.txt"}`, "error: permission denied", 1250*time.Millisecond)
	if record.Success || record.Error == "" {
		t.Fatalf("error trace = %#v", record)
	}
	if record.DurationMS != 1250 || !strings.Contains(record.Annotation, "secret.txt") {
		t.Fatalf("trace metadata = %#v", record)
	}
}

func TestAnnotateToolCallWithCustomTemplate(t *testing.T) {
	record := NewTraceRecordWithTemplates(
		"terminal",
		`{"command":"go test ./...","workdir":"/workspace"}`,
		"ok",
		0,
		map[string]string{"terminal": "[{status}] 在 {workdir} 运行 {command}"},
	)
	if got, want := record.Annotation, "[成功] 在 /workspace 运行 go test ./..."; got != want {
		t.Fatalf("annotation = %q, want %q", got, want)
	}
}
