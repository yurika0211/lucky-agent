package telegram

import "testing"

func TestSanitizeOutgoingText_PreservesNormalCodeFence(t *testing.T) {
	in := "```c\nwhile (1) {\n    task = pick_next_task();\n}\n```\n\n一句话：Linux 的本质是调度。"
	got := sanitizeOutgoingText(in)
	if got != in {
		t.Fatalf("expected code fence to stay intact.\nwant: %q\ngot:  %q", in, got)
	}
}

func TestSanitizeOutgoingText_RemovesProtocolFenceWrapper(t *testing.T) {
	in := "```json\n{\"tool\":\"cron_add\",\"arguments\":{\"schedule\":\"每2小时\"}}\n```\n设置完成。"
	got := sanitizeOutgoingText(in)
	want := "设置完成。"
	if got != want {
		t.Fatalf("unexpected result.\nwant: %q\ngot:  %q", want, got)
	}
}

func TestSanitizeOutgoingText_PreservesBracesAfterProtocolInsideCodeFence(t *testing.T) {
	in := "我先检查一下。\n" +
		"to=shell\n" +
		"{\"command\":\"go test ./...\"}\n" +
		"```go\n" +
		"func main() {\n" +
		"\tif ready {\n" +
		"\t\tstart()\n" +
		"\t}\n" +
		"}\n" +
		"```\n" +
		"检查完成。"

	got := sanitizeOutgoingText(in)
	want := "我先检查一下。\n```go\nfunc main() {\n\tif ready {\n\t\tstart()\n\t}\n}\n```\n检查完成。"
	if got != want {
		t.Fatalf("unexpected result.\nwant: %q\ngot:  %q", want, got)
	}
}
