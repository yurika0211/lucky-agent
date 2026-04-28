package telegram

import "testing"

func TestSanitizeOutgoingText_RemovesToolCallProtocolNoise(t *testing.T) {
	in := `to=shell
{"command":"grep -R \"cron_add\" -n . | head -50"}
<tool_call>
{"name":"cron_add","arguments":{"schedule":"每2小时","mode":"agent","prompt":"..."}}
</tool_call>
`
	got := sanitizeOutgoingText(in)
	if got != internalOutputFilteredFallback {
		t.Fatalf("expected fallback message, got: %q", got)
	}
}

func TestSanitizeOutgoingText_KeepsNormalText(t *testing.T) {
	in := "好的，我已经帮你创建定时任务，每2小时执行一次。"
	got := sanitizeOutgoingText(in)
	if got != in {
		t.Fatalf("expected original text, got: %q", got)
	}
}

func TestSanitizeOutgoingText_RemovesMixedNoiseButKeepsAnswer(t *testing.T) {
	in := `我先帮你设置任务。
to=cron_add
{"tool":"cron_add","arguments":{"schedule":"每2小时"}}
设置完成。`
	got := sanitizeOutgoingText(in)
	want := "我先帮你设置任务。\n设置完成。"
	if got != want {
		t.Fatalf("unexpected result.\nwant: %q\ngot:  %q", want, got)
	}
}

func TestSanitizeOutgoingText_RemovesBraceWrappedProtocolFragments(t *testing.T) {
	in := `我帮你查一下：
{to=cron_status 北京赛车的 code}
{to=autonomy_status 北京赛车的 code}
{to=current_time 无码不卡高清免费观看 code} ! {}
{to=cron_status}
{}
tool
cron_status
{to=cron_status}
对，还没真正挂上。前面大概率只是切了模式/配置，没把任务落到调度器里。`
	got := sanitizeOutgoingText(in)
	want := "我帮你查一下：\n对，还没真正挂上。前面大概率只是切了模式/配置，没把任务落到调度器里。"
	if got != want {
		t.Fatalf("unexpected result.\nwant: %q\ngot:  %q", want, got)
	}
}
