package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent-runtime/gateway"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/cron"
)

/*
cronTaskMode 表示定时任务的执行模式。
*/
type cronTaskMode string

const (
	cronTaskModeShell cronTaskMode = "shell"
	cronTaskModeAgent cronTaskMode = "agent"
)

func getCronNotificationSystemPrompt() string {
	loader := getPromptLoader()
	defaultPrompt := `【后台任务开场设定】
你是我非常信任、观察力敏锐且语气自然的私人助理。你刚刚在后台为我跑完了一项定时任务（Cron Job），现在需要写一段发给用户的简短开场。

输出要求：
- 只写 1 到 2 句话，说明你刚完成了什么，以及结果大体是成功还是失败。
- 不要复述完整原始结果，系统会在你的开场后面自动附上完整内容。
- 不要说"要不要我发原文""要不要我推送完整内容"这类话，因为完整内容会直接发送。
- 不要杜撰不存在的结果。
- 默认用中文，语气自然。`

	return loader.LoadOrDefault("functions/cron_notification.md", defaultPrompt)
}

var cronAgentDisabledTools = []string{
	"cron",
}

const cronNotificationForwardChunkLimit = 1200

type cronNotificationPayload struct {
	JobID     string
	Mode      string
	Command   string
	Outcome   string
	RawResult string
}

/*
saveCronJobs 将当前 cron 引擎中的任务持久化到存储。
*/
func (a *Agent) saveCronJobs() error {
	if a == nil || a.cronStore == nil || a.cronEngine == nil {
		return nil
	}
	return a.cronStore.Save(a.cronEngine)
}

func (a *Agent) installCronEventHandler() {
	if a == nil || a.cronEngine == nil {
		return
	}
	a.cronEngine.SetEventHandler(func(event cron.Event) {
		switch event.Type {
		case cron.EventJobStarted:
			fmt.Printf("[cron] job %s started\n", event.JobName)
		case cron.EventJobCompleted:
			fmt.Printf("[cron] job %s completed\n", event.JobName)
			a.recordCronRun(event, nil)
			if err := a.saveCronJobs(); err != nil {
				fmt.Printf("[cron] save failed: %v\n", err)
			}
		case cron.EventJobFailed:
			fmt.Printf("[cron] job %s failed: %v\n", event.JobName, event.Error)
			a.recordCronRun(event, event.Error)
			if err := a.saveCronJobs(); err != nil {
				fmt.Printf("[cron] save failed: %v\n", err)
			}
		}
	})
}

// recordCronRun separates a command failure from a delivery failure and stops
// a one-shot job from being rescheduled forever. The engine has already stored
// the next run; this corrects that product state before it is persisted.
func (a *Agent) recordCronRun(event cron.Event, runErr error) {
	if a == nil || a.cronEngine == nil {
		return
	}
	// GetJob returns a shallow copy whose Metadata map is the engine's map.
	// Classification is written there so the next save persists it. Status and
	// NextRun still go through RestoreJobState.
	job, ok := a.cronEngine.GetJob(event.JobID)
	if !ok || job == nil || job.Metadata == nil {
		return
	}
	meta := job.Metadata
	class, deliveryStatus, deliveryErr := classifyCronRunError(runErr)
	if class == cronErrorClassDelivery && deliveryStatus == cronDeliveryUndelivered {
		// The command finished. A missing gateway must not keep the task failed.
		meta[cronMetaLastErrorClass] = cronErrorClassDelivery
		meta[cronMetaDeliveryStatus] = cronDeliveryUndelivered
		meta[cronMetaDeliveryError] = deliveryErr
		meta[cronMetaRetryCount] = "0"
		_ = a.cronEngine.RestoreJobState(event.JobID, cronStateClearingTaskFailure(job))
		return
	}
	meta[cronMetaDeliveryStatus] = deliveryStatus
	meta[cronMetaDeliveryError] = deliveryErr
	if class == cronErrorClassNone {
		meta[cronMetaLastErrorClass] = cronErrorClassNone
		meta[cronMetaRetryCount] = "0"
		return
	}

	retries := cronMetadataInt(meta, cronMetaRetryCount) + 1
	meta[cronMetaRetryCount] = strconv.Itoa(retries)
	kind := cronScheduleKind(job.Schedule, meta)
	if kind == cronScheduleKindOneShot && retries >= cronOneShotRetryLimit {
		meta[cronMetaLastErrorClass] = cronErrorClassExhausted
		state := restoredCronState(job)
		state.Status = cron.StatusFailed.String()
		state.NextRun = cronClearedNextRun()
		_ = a.cronEngine.RestoreJobState(event.JobID, state)
		return
	}
	meta[cronMetaLastErrorClass] = cronErrorClassCommand
	if kind == cronScheduleKindOneShot {
		when := time.Now().Add(cronOneShotBackoff(retries))
		state := restoredCronState(job)
		state.Status = cron.StatusIdle.String()
		state.NextRun = &when
		_ = a.cronEngine.RestoreJobState(event.JobID, state)
	}
}

func classifyCronRunError(err error) (class, deliveryStatus, deliveryErr string) {
	if err == nil {
		return cronErrorClassNone, cronDeliverySkipped, ""
	}
	var delivery *cronDeliveryError
	var task *cronTaskError
	switch {
	case errors.As(err, &delivery) && !errors.As(err, &task):
		return cronErrorClassDelivery, cronDeliveryUndelivered, delivery.Error()
	case errors.As(err, &task):
		return cronErrorClassCommand, cronDeliverySkipped, ""
	default:
		// Errors restored from older runs have no typed class. Treat them as
		// command failures so a delivery miss is never inferred from text.
		return cronErrorClassCommand, cronDeliverySkipped, ""
	}
}

func cronOneShotBackoff(retry int) time.Duration {
	if retry < 1 {
		retry = 1
	}
	delay := cronOneShotRetryBase
	for i := 1; i < retry; i++ {
		delay *= 2
	}
	if delay > 15*time.Minute {
		return 15 * time.Minute
	}
	return delay
}

func restoredCronState(job *cron.Job) cron.RestoredJobState {
	state := cron.RestoredJobState{
		Status:         job.Status.String(),
		Paused:         job.Status == cron.StatusPaused,
		RunCount:       job.RunCount,
		ErrorCount:     job.ErrorCount,
		LastError:      job.LastError,
		RunHistory:     append([]cron.RunRecord(nil), job.RunHistory...),
		DeleteAfterRun: job.DeleteAfterRun,
	}
	if !job.CreatedAt.IsZero() {
		created := job.CreatedAt
		state.CreatedAt = &created
	}
	if !job.LastRun.IsZero() {
		last := job.LastRun
		state.LastRun = &last
	}
	if !job.NextRun.IsZero() {
		next := job.NextRun
		state.NextRun = &next
	}
	updated := time.Now()
	state.UpdatedAt = &updated
	return state
}

// cronStateClearingTaskFailure keeps the successful command while recording
// that its result was not delivered. One-shot jobs still end; they do not
// gain another run because the gateway was missing.
func cronStateClearingTaskFailure(job *cron.Job) cron.RestoredJobState {
	state := restoredCronState(job)
	if job.ErrorCount > 0 {
		state.ErrorCount = job.ErrorCount - 1
	}
	state.LastError = ""
	if cronScheduleKind(job.Schedule, job.Metadata) == cronScheduleKindOneShot || job.NextRun.IsZero() {
		state.Status = cron.StatusDone.String()
		state.NextRun = cronClearedNextRun()
	} else {
		state.Status = cron.StatusIdle.String()
	}
	return state
}

// cronClearedNextRun is a non-nil zero time. RestoreJobState leaves NextRun
// unchanged when the pointer itself is nil, so clearing the schedule needs a
// pointer to the zero time.
func cronClearedNextRun() *time.Time {
	cleared := time.Time{}
	return &cleared
}

func cronMetadataInt(meta map[string]string, key string) int {
	if meta == nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(meta[key]))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

/*
restoreCronJobs 从持久化存储恢复 cron 任务。
*/
func (a *Agent) restoreCronJobs() (int, error) {
	if a == nil || a.cronStore == nil || a.cronEngine == nil {
		return 0, nil
	}
	restored, err := a.cronStore.Load(a.cronEngine, a.rebuildCronTask)
	// Load reschedules a paused job whose stored next run is already due.
	// Pause still means "do not schedule", including across a restart.
	a.quiescePausedCronJobs()
	return restored, err
}

func (a *Agent) rebuildCronTask(job cron.PersistedJob) (func() error, map[string]string, error) {
	mode := cronTaskMode(strings.ToLower(strings.TrimSpace(job.Mode)))
	switch mode {
	case cronTaskModeAgent, cronTaskModeShell:
	default:
		mode = cronTaskModeShell
	}

	command := strings.TrimSpace(job.Command)
	if command == "" {
		return nil, nil, fmt.Errorf("command is empty")
	}

	metadata := map[string]string{
		"mode": mode.String(),
	}
	for k, v := range job.Metadata {
		metadata[k] = v
	}
	return a.buildCronTask(job.ID, mode, command, metadata), metadata, nil
}

// quiescePausedCronJobs clears the next run of every paused job. Pause means
// the scheduler will not run it; a stored next_run must not make a stopped
// draft look like tomorrow's work.
func (a *Agent) quiescePausedCronJobs() {
	if a == nil || a.cronEngine == nil {
		return
	}
	for _, job := range a.cronEngine.ListJobs() {
		if job == nil || job.Status != cron.StatusPaused || job.NextRun.IsZero() {
			continue
		}
		// Recurring jobs keep their next run so resume can restore the same
		// schedule. One-shot drafts must not stay due after pause.
		if cronScheduleKind(job.Schedule, job.Metadata) != cronScheduleKindOneShot {
			continue
		}
		state := restoredCronState(job)
		state.Status = cron.StatusPaused.String()
		state.Paused = true
		state.NextRun = cronClearedNextRun()
		_ = a.cronEngine.RestoreJobState(job.ID, state)
	}
}

/*
String 返回 cronTaskMode 的字符串形式。
*/
func (m cronTaskMode) String() string {
	return string(m)
}

// installCronToolPolicy wraps the published cron tool. The tool package mints
// a new id on every add; this layer returns the existing job when the intent
// key matches, and lists paused one-shot drafts as one group.
func (a *Agent) installCronToolPolicy() {
	if a == nil || a.tools == nil {
		return
	}
	current, ok := a.tools.Get("cron")
	if !ok || current == nil || current.Handler == nil {
		return
	}
	base := current.Handler
	wrapped := *current
	params := make(map[string]tool.Param, len(current.Parameters)+8)
	for key, value := range current.Parameters {
		params[key] = value
	}
	params["title"] = tool.Param{Type: "string", Description: "Short task title", Required: false}
	params["intent"] = tool.Param{Type: "string", Description: "Stable user intent. Repeat adds with the same intent return the existing job", Required: false}
	params["schedule_kind"] = tool.Param{Type: "string", Description: "one_shot or recurring. Defaults from the schedule", Required: false}
	params["created_by"] = tool.Param{Type: "string", Description: "Who created the task", Required: false}
	params["desired_outcome"] = tool.Param{Type: "string", Description: "What success looks like", Required: false}
	params["idempotency_key"] = tool.Param{Type: "string", Description: "Explicit intent key. A second add with this key returns the existing job", Required: false}
	params["intent_key"] = tool.Param{Type: "string", Description: "For action=archive, archive every paused draft with this intent", Required: false}
	wrapped.Parameters = params
	wrapped.Description = strings.TrimSpace(current.Description + " Repeat add of the same intent returns the existing job. action=archive hides paused one-shot drafts that share an intent_key. The default list folds those drafts.")
	wrapped.Handler = func(args map[string]any) (string, error) {
		return a.handleCronTool(base, args)
	}
	a.tools.Register(&wrapped)
}

func (a *Agent) handleCronTool(base func(map[string]any) (string, error), args map[string]any) (string, error) {
	action, _ := args["action"].(string)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "add":
		return a.handleCronToolAdd(base, args)
	case "list":
		return a.handleCronToolList(base, args)
	case "archive":
		return a.handleCronToolArchive(args)
	default:
		return base(args)
	}
}

func (a *Agent) handleCronToolAdd(base func(map[string]any) (string, error), args map[string]any) (string, error) {
	modeText, _ := args["mode"].(string)
	command, _ := args["command"].(string)
	command = strings.TrimSpace(command)
	mode, err := parseCronToolMode(modeText)
	if err != nil || command == "" {
		return base(args)
	}
	scheduleText, _ := args["schedule"].(string)
	schedule, scheduleErr := cron.ParseNaturalLanguage(strings.TrimSpace(scheduleText))
	if scheduleErr != nil {
		schedule, scheduleErr = cron.ParseCronExpr(strings.TrimSpace(scheduleText))
	}
	meta := cronToolIdentityArgs(args)
	key, prepared := prepareCronIdentity(cronToolString(args, "id"), cronToolString(args, "title"), cronToolString(args, "intent"), mode, command, meta, schedule)
	if a.cronEngine != nil {
		if existing := findCronJobByIntent(a.cronEngine.ListJobs(), key); existing != nil {
			return cronExistingJobJSON(existing), nil
		}
	}
	if scheduleErr == nil && schedule != nil && strings.TrimSpace(cronToolString(args, "id")) == "" {
		// One intent keeps one id. A later retry must not append -2 or a new timestamp.
		args["id"] = "intent-" + key
	}
	for field, metaKey := range map[string]string{
		"title": prepared[cronMetaTitle], "intent": prepared[cronMetaIntent],
	} {
		if strings.TrimSpace(cronToolString(args, field)) == "" && metaKey != "" {
			args[field] = metaKey
		}
	}
	out, err := base(args)
	if err != nil || a.cronEngine == nil {
		return out, err
	}
	id := cronToolString(args, "id")
	if id == "" {
		var payload map[string]any
		if json.Unmarshal([]byte(out), &payload) == nil {
			id, _ = payload["id"].(string)
		}
	}
	if job, ok := a.cronEngine.GetJob(id); ok && job != nil && job.Metadata != nil {
		for key, value := range prepared {
			if strings.TrimSpace(job.Metadata[key]) == "" {
				job.Metadata[key] = value
			}
		}
		_ = a.saveCronJobs()
	}
	return out, nil
}

func (a *Agent) handleCronToolList(base func(map[string]any) (string, error), args map[string]any) (string, error) {
	out, err := base(args)
	if err != nil || a == nil || a.cronEngine == nil {
		return out, err
	}
	view := buildCronListView(a.cronEngine.ListJobs())
	var payload map[string]any
	if json.Unmarshal([]byte(out), &payload) != nil {
		payload = map[string]any{}
	}
	payload["recurring"] = view.Recurring
	payload["one_shot"] = view.OneShot
	payload["paused_drafts"] = view.PausedDrafts
	payload["recurring_count"] = view.RecurringCount
	payload["one_shot_count"] = view.OneShotCount
	payload["paused_draft_groups"] = view.DraftGroups
	payload["paused_draft_jobs"] = view.DraftJobs
	payload["jobs"] = append(append([]cronListItem{}, view.Recurring...), view.OneShot...)
	payload["total"] = view.RecurringCount + view.OneShotCount
	raw, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return out, nil
	}
	return string(raw), nil
}

func (a *Agent) handleCronToolArchive(args map[string]any) (string, error) {
	if a == nil || a.cronEngine == nil {
		return "", fmt.Errorf("cron engine is not initialized")
	}
	key := firstNonEmpty(cronToolString(args, "intent_key"), cronToolString(args, "idempotency_key"))
	if key == "" {
		return "", fmt.Errorf("intent_key is required")
	}
	ids := archiveCronIntent(a.cronEngine.ListJobs(), key)
	if err := a.saveCronJobs(); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(map[string]any{
		"ok":         true,
		"action":     "archive",
		"intent_key": key,
		"archived":   ids,
		"count":      len(ids),
	})
	return string(raw), nil
}

func cronExistingJobJSON(job *cron.Job) string {
	raw, _ := json.Marshal(map[string]any{
		"ok":              true,
		"action":          "add",
		"existing":        true,
		"id":              job.ID,
		"intent_key":      cronJobIntentKey(job),
		"idempotency_key": cronJobIntentKey(job),
		"title":           cronMetaOr(job, cronMetaTitle, job.Name),
		"message":         fmt.Sprintf("Scheduled job %s already exists for this intent", job.ID),
	})
	return string(raw)
}

func cronToolIdentityArgs(args map[string]any) map[string]string {
	meta := map[string]string{}
	for _, key := range []string{
		"title", "intent", "schedule_kind", "created_by", "desired_outcome",
		"idempotency_key", "session_id", "platform", "delete_after_run",
	} {
		if value := cronToolString(args, key); value != "" {
			meta[key] = value
		}
	}
	return meta
}

func cronToolString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func parseCronToolMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "shell":
		return "shell", nil
	case "agent":
		return "agent", nil
	default:
		return "", fmt.Errorf("invalid cron mode %q", mode)
	}
}

func (a *Agent) buildCronTask(id string, mode cronTaskMode, command string, metadata map[string]string) func() error {
	runtime := cronTaskRuntime{
		loop:     a.loopRunner(),
		sessions: a.sessions,
		cfg:      a.cfg,
		gateway:  a.gateway,
		notify:   a.sendCronNotification,
	}
	return runtime.buildTask(id, mode, command, metadata)
}

func combineCronTaskAndNotificationError(taskErr, notificationErr error) error {
	var task *cronTaskError
	if taskErr != nil {
		task = &cronTaskError{err: taskErr}
	}
	if notificationErr == nil {
		return task
	}
	delivery := &cronDeliveryError{err: notificationErr}
	if task == nil {
		return delivery
	}
	// Both are returned so logs still show the command error. recordCronRun
	// classifies the command failure first and does not retry delivery alone.
	return errors.Join(task, delivery)
}

func (a *Agent) sendCronNotification(metadata map[string]string, payload cronNotificationPayload) error {
	message := a.formatCronNotification(payload)
	if a == nil || strings.TrimSpace(message) == "" {
		return nil
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	sessionID := strings.TrimSpace(metadata["session_id"])
	platformHint := strings.ToLower(strings.TrimSpace(metadata["platform"]))
	if sessionID != "" && (platformHint == "" || platformHint == "android" || platformHint == "app") {
		if err := a.deliverCronToSession(sessionID, message); err != nil {
			return err
		}
		return nil
	}
	hasExplicitTarget := firstCronMetadataValue(metadata, "platform", "chatID", "chat_id") != ""
	if a.msgGateway == nil {
		if hasExplicitTarget {
			return fmt.Errorf("message gateway is not initialized")
		}
		return nil
	}
	platform := strings.TrimSpace(metadata["platform"])
	chatID := firstCronMetadataValue(metadata, "chatID", "chat_id")
	replyToMsgID := firstCronMetadataValue(metadata, "replyToMsgID", "reply_to_message_id")
	if platform == "" || chatID == "" {
		target := a.pickRecentChatTarget()
		if platform == "" {
			platform = strings.TrimSpace(target.Platform)
		}
		if chatID == "" {
			chatID = strings.TrimSpace(target.ChatID)
		}
		if replyToMsgID == "" {
			replyToMsgID = strings.TrimSpace(target.ReplyToMsgID)
		}
	}
	if platform == "" || chatID == "" {
		return nil
	}
	gw, ok := a.msgGateway.Get(platform)
	if !ok || gw == nil {
		return fmt.Errorf("gateway %q is not registered", platform)
	}
	if !gw.IsRunning() {
		return fmt.Errorf("gateway %q is not running", platform)
	}
	sendCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if forwarder, ok := gw.(gateway.ForwardedTextSender); ok && cronNotificationShouldForward(message) {
		if err := forwarder.SendForwardedText(sendCtx, chatID, "LuckyAgent", splitCronNotificationChunks(message, cronNotificationForwardChunkLimit)); err == nil {
			return nil
		}
	}
	if receiptGW, ok := gw.(gateway.ReceiptGateway); ok {
		var (
			sent gateway.SentMessage
			err  error
		)
		if replyToMsgID != "" {
			sent, err = receiptGW.SendWithReplyReceipt(sendCtx, chatID, replyToMsgID, message)
		} else {
			sent, err = receiptGW.SendWithReceipt(sendCtx, chatID, message)
		}
		if err == nil && strings.TrimSpace(sent.ID) != "" && sessionID != "" {
			a.RecordExternalReplyAnchor(platform, chatID, sent.ID, sessionID, payload.JobID)
		}
		if err != nil {
			return fmt.Errorf("send via %s to %s: %w", platform, chatID, err)
		}
		return nil
	}
	if replyToMsgID != "" {
		if err := gw.SendWithReply(sendCtx, chatID, replyToMsgID, message); err != nil {
			return fmt.Errorf("send via %s to %s: %w", platform, chatID, err)
		}
		return nil
	}
	if err := gw.Send(sendCtx, chatID, message); err != nil {
		return fmt.Errorf("send via %s to %s: %w", platform, chatID, err)
	}
	return nil
}

func (a *Agent) deliverCronToSession(sessionID, message string) error {
	sessionID = strings.TrimSpace(sessionID)
	message = strings.TrimSpace(message)
	if a == nil || a.sessions == nil || sessionID == "" || message == "" {
		return nil
	}
	sess, ok := a.sessions.Get(sessionID)
	if !ok || sess == nil {
		return fmt.Errorf("android session %q not found", sessionID)
	}
	sess.AddMessage("assistant", message)
	if err := sess.Save(); err != nil {
		return fmt.Errorf("save android session %s: %w", sessionID, err)
	}
	if a.sessionEvents != nil {
		a.sessionEvents(sessionID, message)
	}
	return nil
}

func cronNotificationShouldForward(message string) bool {
	return len([]rune(strings.TrimSpace(message))) > cronNotificationForwardChunkLimit
}

func splitCronNotificationChunks(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if limit <= 0 || len([]rune(text)) <= limit {
		return []string{text}
	}

	var chunks []string
	var current strings.Builder
	for _, line := range strings.Split(text, "\n") {
		for len([]rune(line)) > limit {
			if strings.TrimSpace(current.String()) != "" {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
			}
			head, tail := splitCronNotificationRunes(line, limit)
			chunks = append(chunks, strings.TrimSpace(head))
			line = tail
		}

		extra := len([]rune(line))
		if current.Len() > 0 {
			extra++
		}
		if current.Len() > 0 && len([]rune(current.String()))+extra > limit {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte('\n')
		}
		current.WriteString(line)
	}
	if strings.TrimSpace(current.String()) != "" {
		chunks = append(chunks, strings.TrimSpace(current.String()))
	}
	return chunks
}

func splitCronNotificationRunes(text string, limit int) (string, string) {
	runes := []rune(text)
	if limit >= len(runes) {
		return text, ""
	}
	return string(runes[:limit]), string(runes[limit:])
}

func firstCronMetadataValue(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			return value
		}
	}
	return ""
}

func (a *Agent) formatCronNotification(payload cronNotificationPayload) string {
	fallback := buildCronNotificationMessage(fallbackCronNotificationIntro(payload), payload)
	turnProvider := a.baseProviderSnapshot()
	if !turnProvider.valid() {
		return fallback
	}

	rawResult := strings.TrimSpace(payload.RawResult)
	if len(rawResult) > 4000 {
		rawResult = rawResult[:4000] + "\n...（结果较长，开场只参考了前半部分，完整内容会在下方附上）"
	}

	var userPrompt strings.Builder
	userPrompt.WriteString("下面是一次后台定时任务的执行信息，请按设定写一段简短开场。\n\n")
	userPrompt.WriteString("job_id: ")
	userPrompt.WriteString(strings.TrimSpace(payload.JobID))
	userPrompt.WriteString("\nmode: ")
	userPrompt.WriteString(strings.TrimSpace(payload.Mode))
	userPrompt.WriteString("\ncommand: ")
	userPrompt.WriteString(strings.TrimSpace(payload.Command))
	userPrompt.WriteString("\noutcome: ")
	userPrompt.WriteString(strings.TrimSpace(payload.Outcome))
	userPrompt.WriteString("\n\n原始执行结果：\n")
	userPrompt.WriteString(rawResult)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := a.chatLoopIteration(ctx, []provider.Message{
		{Role: "system", Content: getCronNotificationSystemPrompt()},
		{Role: "user", Content: userPrompt.String()},
	}, provider.CallOptions{}, true, turnProvider)
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		return fallback
	}
	intro := strings.TrimSpace(resp.Content)
	if strings.Contains(intro, "要不要") {
		intro = fallbackCronNotificationIntro(payload)
	}
	return buildCronNotificationMessage(intro, payload)
}

func fallbackCronNotificationIntro(payload cronNotificationPayload) string {
	jobID := strings.TrimSpace(payload.JobID)
	command := strings.TrimSpace(payload.Command)

	if jobID == "" {
		jobID = "unknown-job"
	}
	if command == "" {
		command = "这项后台任务"
	}

	switch strings.ToLower(strings.TrimSpace(payload.Outcome)) {
	case "failed":
		return fmt.Sprintf("我刚刚替你跑了一下定时任务\"%s\"，不过这次没顺利完成。完整错误我直接贴在下面。", command)
	default:
		return fmt.Sprintf("我刚刚把定时任务\"%s\"跑完了。完整结果我直接贴在下面。", command)
	}
}

func buildCronNotificationMessage(intro string, payload cronNotificationPayload) string {
	intro = strings.TrimSpace(intro)
	rawResult := strings.TrimSpace(payload.RawResult)
	if intro == "" {
		intro = fallbackCronNotificationIntro(payload)
	}
	if rawResult == "" {
		return intro
	}

	heading := "完整结果"
	if strings.EqualFold(strings.TrimSpace(payload.Outcome), "failed") {
		heading = "完整错误"
	}
	return fmt.Sprintf("%s\n\n%s：\n%s", intro, heading, rawResult)
}
