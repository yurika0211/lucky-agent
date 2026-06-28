package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	appheartbeat "github.com/yurika0211/luckyagent/internal/agent/heartbeat"
	"github.com/yurika0211/luckyagent/internal/session"
)

/*
recentChatTarget 记录最近一次可回投消息的外部聊天目标。
*/
type recentChatTarget struct {
	Platform     string
	ChatID       string
	ReplyToMsgID string
	UpdatedAt    time.Time
}

type externalReplyAnchor struct {
	Platform  string
	ChatID    string
	MessageID string
	SessionID string
	JobID     string
	UpdatedAt time.Time
}

/*
initHeartbeatService 初始化并启动 HEARTBEAT.md 驱动的心跳服务。
*/
func (a *Agent) initHeartbeatService() error {
	if a == nil || a.cfg == nil || a.provider == nil {
		return nil
	}

	cfg := a.cfg.Get()
	enabled := true
	if raw := strings.TrimSpace(cfg.Extra["heartbeat.enabled"]); raw != "" {
		if parsed, err := strconv.ParseBool(strings.ToLower(raw)); err == nil {
			enabled = parsed
		}
	}
	interval := 30 * time.Minute
	if raw := strings.TrimSpace(cfg.Extra["heartbeat.interval_seconds"]); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			interval = time.Duration(seconds) * time.Second
		}
	}

	svc := appheartbeat.New(appheartbeat.Config{
		Workspace: filepath.Join(a.cfg.HomeDir(), "workspace"),
		Provider:  a.provider,
		Model:     a.activeModel,
		Enabled:   enabled,
		Interval:  interval,
		OnExecute: a.executeHeartbeatTasks,
		OnNotify:  a.notifyHeartbeatResponse,
	})
	if err := svc.EnsureWorkspace(); err != nil {
		return err
	}
	a.heartbeatSvc = svc
	return svc.Start()
}

func (a *Agent) RecordRecentChatTarget(platform, chatID, replyToMsgID string) {
	if a == nil {
		return
	}
	platform = strings.TrimSpace(platform)
	chatID = strings.TrimSpace(chatID)
	if platform == "" || chatID == "" {
		return
	}

	a.heartbeatMu.Lock()
	a.recentTarget = recentChatTarget{
		Platform:     platform,
		ChatID:       chatID,
		ReplyToMsgID: strings.TrimSpace(replyToMsgID),
		UpdatedAt:    time.Now(),
	}
	a.heartbeatMu.Unlock()
}

/*
pickRecentChatTarget 读取最近记录的外部聊天目标。
*/
func (a *Agent) pickRecentChatTarget() recentChatTarget {
	a.heartbeatMu.Lock()
	defer a.heartbeatMu.Unlock()
	return a.recentTarget
}

func externalReplyAnchorKey(platform, chatID, messageID string) string {
	platform = strings.ToLower(strings.TrimSpace(platform))
	chatID = strings.TrimSpace(chatID)
	messageID = strings.TrimSpace(messageID)
	if platform == "" || chatID == "" || messageID == "" {
		return ""
	}
	return platform + "\x00" + chatID + "\x00" + messageID
}

func (a *Agent) RecordExternalReplyAnchor(platform, chatID, messageID, sessionID, jobID string) {
	if a == nil {
		return
	}
	key := externalReplyAnchorKey(platform, chatID, messageID)
	sessionID = strings.TrimSpace(sessionID)
	if key == "" || sessionID == "" {
		return
	}

	a.heartbeatMu.Lock()
	if a.externalReplyAnchors == nil {
		a.externalReplyAnchors = make(map[string]externalReplyAnchor)
	}
	a.externalReplyAnchors[key] = externalReplyAnchor{
		Platform:  strings.ToLower(strings.TrimSpace(platform)),
		ChatID:    strings.TrimSpace(chatID),
		MessageID: strings.TrimSpace(messageID),
		SessionID: sessionID,
		JobID:     strings.TrimSpace(jobID),
		UpdatedAt: time.Now(),
	}
	a.heartbeatMu.Unlock()
}

func (a *Agent) ResolveExternalReplyAnchor(platform, chatID, messageID string) (sessionID string, ok bool) {
	if a == nil {
		return "", false
	}
	key := externalReplyAnchorKey(platform, chatID, messageID)
	if key == "" {
		return "", false
	}

	a.heartbeatMu.Lock()
	defer a.heartbeatMu.Unlock()
	anchor, ok := a.externalReplyAnchors[key]
	if !ok || strings.TrimSpace(anchor.SessionID) == "" {
		return "", false
	}
	return anchor.SessionID, true
}

/*
heartbeatSession 获取或创建心跳专用会话。
*/
func (a *Agent) heartbeatSession() *session.Session {
	a.heartbeatMu.Lock()
	defer a.heartbeatMu.Unlock()

	if a.heartbeatSessionID != "" {
		if sess, ok := a.sessions.Get(a.heartbeatSessionID); ok {
			return sess
		}
	}

	sess := a.sessions.NewWithTitle("heartbeat")
	a.heartbeatSessionID = sess.ID
	return sess
}

/*
executeHeartbeatTasks 通过 Agent Loop 执行心跳任务文本。
*/
func (a *Agent) executeHeartbeatTasks(ctx context.Context, tasks string) (string, error) {
	sess := a.heartbeatSession()
	loopCfg := DefaultLoopConfig()
	if a.cfg != nil {
		ApplyAgentLoopConfig(&loopCfg, a.cfg.Get().Agent)
	}
	loopCfg.AutoApprove = true

	result, err := a.RunLoopWithSession(ctx, sess, tasks, loopCfg)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Response), nil
}

/*
notifyHeartbeatResponse 将心跳结果发送到最近活跃的聊天目标。
*/
func (a *Agent) notifyHeartbeatResponse(ctx context.Context, response string) error {
	if a == nil || a.msgGateway == nil {
		return nil
	}
	target := a.pickRecentChatTarget()
	if target.Platform == "" || target.ChatID == "" {
		return nil
	}
	gw, ok := a.msgGateway.Get(target.Platform)
	if !ok || gw == nil || !gw.IsRunning() {
		return nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if target.ReplyToMsgID != "" {
		return gw.SendWithReply(sendCtx, target.ChatID, target.ReplyToMsgID, response)
	}
	return gw.Send(sendCtx, target.ChatID, response)
}

func (a *Agent) handleHeartbeatTrigger(args map[string]any) (string, error) {
	if a == nil || a.heartbeatSvc == nil {
		return "", fmt.Errorf("heartbeat service is not initialized")
	}
	response, err := a.heartbeatSvc.TriggerNow(context.Background())
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"triggered": true,
		"response":  response,
	})
	return string(out), nil
}

func (a *Agent) handleHeartbeatStatus(args map[string]any) (string, error) {
	target := a.pickRecentChatTarget()
	path := ""
	if a.heartbeatSvc != nil {
		path = a.heartbeatSvc.HeartbeatFile()
	}
	out, _ := json.Marshal(map[string]any{
		"enabled":           a.heartbeatSvc != nil,
		"heartbeat_file":    path,
		"recent_platform":   target.Platform,
		"recent_chat_id":    target.ChatID,
		"recent_updated_at": target.UpdatedAt,
	})
	return string(out), nil
}
