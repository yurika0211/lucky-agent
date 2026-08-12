package lhcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/session"
)

func newSessionCmd() *cobra.Command {
	sessionCmd := &cobra.Command{
		Use:   "session",
		Short: "管理会话",
	}

	var dryRun bool
	var forceLocal bool
	var keepAfter bool
	compactCmd := &cobra.Command{
		Use:   "compact <session-id>",
		Short: "压缩指定会话历史",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionCompact(cmd.Context(), args[0], dryRun, forceLocal)
		},
	}
	compactCmd.Flags().BoolVar(&dryRun, "dry-run", false, "生成 compact 结果但不写入会话")
	compactCmd.Flags().BoolVar(&forceLocal, "force-local", false, "使用本地 fallback summary 写入 compact boundary")
	compactUndoCmd := &cobra.Command{
		Use:   "undo <session-id>",
		Short: "撤销指定会话最近一次 compact boundary",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionCompactUndo(args[0], keepAfter)
		},
	}
	compactUndoCmd.Flags().BoolVar(&keepAfter, "keep-after", false, "boundary 后已有新消息时仍保留后续消息并删除 boundary")
	compactCmd.AddCommand(compactUndoCmd)

	backupCmd := &cobra.Command{
		Use:   "backup",
		Short: "管理会话的独立备份",
	}
	backupListCmd := &cobra.Command{
		Use:   "list <session-id>",
		Short: "列出会话备份",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionBackupList(args[0])
		},
	}
	backupRestoreCmd := &cobra.Command{
		Use:   "restore <session-id> <backup-id>",
		Short: "从独立备份恢复会话",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionBackupRestore(args[0], args[1])
		},
	}
	backupCmd.AddCommand(backupListCmd, backupRestoreCmd)

	sessionCmd.AddCommand(compactCmd, backupCmd)
	return sessionCmd
}

func runSessionCompact(ctx context.Context, sessionID string, dryRun bool, forceLocal bool) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer a.Close()

	sessionMgr := a.Sessions()
	if sessionMgr == nil {
		return fmt.Errorf("session manager is not initialized")
	}
	sess, err := resolveSessionByIDPrefix(sessionMgr, sessionID)
	if err != nil {
		return err
	}

	result, err := a.CompactSessionWithOptions(ctx, sess, "manual-cli", agent.CompactSessionOptions{
		ForceLocal: forceLocal,
		DryRun:     dryRun,
	})
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Printf("compact dry-run: %s\n", sess.ID)
	} else {
		fmt.Printf("session compacted: %s\n", sess.ID)
	}
	fmt.Printf("boundary: %s\n", result.BoundaryID)
	fmt.Printf("covered messages: [%d, %d)\n", result.FromMessage, result.ToMessage)
	fmt.Printf("dropped messages: %d\n", result.DroppedMessages)
	fmt.Printf("retained messages: %d\n", result.RetainedMessages)
	fmt.Printf("restored attachments: %d\n", result.RestoredAttachments)
	fmt.Printf("summary source: %s\n", result.SummarySource)
	fmt.Printf("summary tokens: %d\n", result.SummaryTokens)
	fmt.Printf("tokens: %d -> %d\n", result.PreTokenEstimate, result.PostTokenEstimate)
	if result.Backup != nil {
		fmt.Printf("backup: %s\n", result.Backup.Path)
		fmt.Printf("backup hash: %s\n", result.Backup.ContentHash)
	}
	return nil
}

func runSessionCompactUndo(sessionID string, keepAfter bool) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}

	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer a.Close()

	sessionMgr := a.Sessions()
	if sessionMgr == nil {
		return fmt.Errorf("session manager is not initialized")
	}
	sess, err := resolveSessionByIDPrefix(sessionMgr, sessionID)
	if err != nil {
		return err
	}
	meta, err := sess.UndoLatestCompactBoundary(keepAfter)
	if err != nil {
		return err
	}
	if err := sess.Save(); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	fmt.Printf("compact boundary undone: %s\n", sess.ID)
	fmt.Printf("boundary: %s\n", meta.ID)
	if meta.Trigger != "" {
		fmt.Printf("trigger: %s\n", meta.Trigger)
	}
	return nil
}

func runSessionBackupList(sessionID string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer a.Close()
	sess, err := resolveSessionByIDPrefix(a.Sessions(), sessionID)
	if err != nil {
		return err
	}
	backups, err := sess.ListBackups()
	if err != nil {
		return err
	}
	if len(backups) == 0 {
		fmt.Printf("no backups: %s\n", sess.ID)
		return nil
	}
	for _, backup := range backups {
		fmt.Printf("%s  %s  messages=%d  trigger=%s  hash=%s\n", backup.ID, backup.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), backup.MessageCount, backup.Trigger, backup.ContentHash)
		fmt.Printf("  path: %s\n", backup.Path)
	}
	return nil
}

func runSessionBackupRestore(sessionID, backupID string) error {
	mgr, err := config.NewManager()
	if err != nil {
		return err
	}
	if err := mgr.Load(); err != nil {
		return err
	}
	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer a.Close()
	sess, err := resolveSessionByIDPrefix(a.Sessions(), sessionID)
	if err != nil {
		return err
	}
	backup, err := sess.RestoreBackup(backupID)
	if err != nil {
		return err
	}
	fmt.Printf("session restored: %s\n", sess.ID)
	fmt.Printf("backup: %s\n", backup.ID)
	fmt.Printf("messages: %d\n", backup.MessageCount)
	return nil
}

func resolveSessionByIDPrefix(mgr *session.Manager, idPrefix string) (*session.Session, error) {
	idPrefix = strings.TrimSpace(idPrefix)
	if idPrefix == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if sess, ok := mgr.Get(idPrefix); ok {
		return sess, nil
	}

	var matches []*session.Session
	for _, sess := range mgr.List() {
		if strings.HasPrefix(sess.ID, idPrefix) {
			matches = append(matches, sess)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("session not found: %s", idPrefix)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, 0, len(matches))
		for _, sess := range matches {
			ids = append(ids, sess.ID)
		}
		return nil, fmt.Errorf("session id prefix %q is ambiguous: %s", idPrefix, strings.Join(ids, ", "))
	}
}
