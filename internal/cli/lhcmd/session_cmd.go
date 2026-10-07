package lhcmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/session"
)

func newSessionCmd() *cobra.Command {
	sessionCmd := &cobra.Command{
		Use:     "session",
		Aliases: []string{"sess"},
		Short:   "管理会话",
	}

	var dryRun bool
	var forceLocal bool
	var keepAfter bool
	compactCmd := &cobra.Command{
		Use:     "compact <session-id>",
		Aliases: []string{"c"},
		Short:   "压缩指定会话历史",
		Args:    cobra.ExactArgs(1),
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
		Use:     "backup",
		Aliases: []string{"b"},
		Short:   "管理会话的独立备份",
	}
	backupListCmd := &cobra.Command{
		Use:     "list <session-id>",
		Aliases: []string{"ls"},
		Short:   "列出会话备份",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionBackupList(args[0])
		},
	}
	backupRestoreCmd := &cobra.Command{
		Use:     "restore <session-id> <backup-id>",
		Aliases: []string{"r"},
		Short:   "从独立备份恢复会话",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionBackupRestore(args[0], args[1])
		},
	}
	backupCmd.AddCommand(backupListCmd, backupRestoreCmd)

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "列出会话及格式/体积",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionList()
		},
	}

	var migrateAll bool
	migrateCmd := &cobra.Command{
		Use:   "migrate [session-id]",
		Short: "迁移会话到 segment_v1 并外置大 content",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			return runSessionMigrate(id, migrateAll)
		},
	}
	migrateCmd.Flags().BoolVar(&migrateAll, "all", false, "迁移全部会话")

	var gcAll bool
	gcCmd := &cobra.Command{
		Use:   "gc [session-id]",
		Short: "清理会话未引用的 blob 文件",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			return runSessionGC(id, gcAll)
		},
	}
	gcCmd.Flags().BoolVar(&gcAll, "all", false, "清理全部会话 blob")

	var exportOut string
	var exportTools bool
	var exportSystem bool
	exportCmd := &cobra.Command{
		Use:   "export <session-id>",
		Short: "导出可读 Markdown（默认不含全量 tool dump）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionExport(args[0], exportOut, exportTools, exportSystem)
		},
	}
	exportCmd.Flags().StringVarP(&exportOut, "out", "o", "", "输出文件路径（默认 stdout）")
	exportCmd.Flags().BoolVar(&exportTools, "include-tools", false, "包含 tool 结果预览")
	exportCmd.Flags().BoolVar(&exportSystem, "include-system", false, "包含普通 system 消息")

	var rollRetain int
	var rollForce bool
	rollCmd := &cobra.Command{
		Use:   "roll <session-id>",
		Short: "就地滚动会话：旧消息进 archive，保留最近若干轮",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSessionRoll(args[0], rollRetain, rollForce)
		},
	}
	rollCmd.Flags().IntVar(&rollRetain, "retain-turns", 12, "保留最近 user 轮数")
	rollCmd.Flags().BoolVar(&rollForce, "force", false, "即使未超限也强制 roll")

	sessionCmd.AddCommand(compactCmd, backupCmd, listCmd, migrateCmd, gcCmd, exportCmd, rollCmd)
	return sessionCmd
}

func openSessionAgent() (*agent.Agent, error) {
	mgr, err := config.NewManager()
	if err != nil {
		return nil, err
	}
	if err := mgr.Load(); err != nil {
		return nil, err
	}
	a, err := agent.New(mgr)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}
	return a, nil
}

func runSessionList() error {
	a, err := openSessionAgent()
	if err != nil {
		return err
	}
	defer a.Close()
	infos := a.Sessions().ListInfo()
	if len(infos) == 0 {
		fmt.Println("no sessions")
		return nil
	}
	for _, info := range infos {
		fmt.Printf("%s  msgs=%d  bytes=%d  format=%s  %s\n",
			info.ID, info.MessageCount, info.ByteSize, info.Format, info.Title)
	}
	return nil
}

func runSessionMigrate(idPrefix string, all bool) error {
	if !all && strings.TrimSpace(idPrefix) == "" {
		return fmt.Errorf("provide a session id or --all")
	}
	a, err := openSessionAgent()
	if err != nil {
		return err
	}
	defer a.Close()
	mgr := a.Sessions()
	if all {
		results := mgr.MigrateAll()
		ok, fail := 0, 0
		for _, res := range results {
			if res.Error != "" {
				fail++
				fmt.Printf("FAIL %s: %s\n", res.ID, res.Error)
				continue
			}
			ok++
			fmt.Printf("OK   %s  %s -> %s  msgs=%d  bytes=%d  blobs=%d\n",
				res.ID, res.FromFormat, res.ToFormat, res.MessageCount, res.ByteSize, res.BlobCount)
		}
		fmt.Printf("migrated ok=%d fail=%d\n", ok, fail)
		if fail > 0 {
			return fmt.Errorf("migrate finished with %d failures", fail)
		}
		return nil
	}
	sess, err := resolveSessionByIDPrefix(mgr, idPrefix)
	if err != nil {
		return err
	}
	res, err := sess.MigrateToSegment()
	if err != nil {
		return err
	}
	fmt.Printf("migrated %s  %s -> %s  msgs=%d  bytes=%d  blobs=%d\n",
		res.ID, res.FromFormat, res.ToFormat, res.MessageCount, res.ByteSize, res.BlobCount)
	return nil
}

func runSessionGC(idPrefix string, all bool) error {
	if !all && strings.TrimSpace(idPrefix) == "" {
		return fmt.Errorf("provide a session id or --all")
	}
	a, err := openSessionAgent()
	if err != nil {
		return err
	}
	defer a.Close()
	mgr := a.Sessions()
	if all {
		results := mgr.GCAllBlobs()
		var removed int
		var freed int64
		for _, res := range results {
			if res.Error != "" {
				fmt.Printf("FAIL %s: %s\n", res.ID, res.Error)
				continue
			}
			if res.Removed == 0 {
				continue
			}
			removed += res.Removed
			freed += res.FreedBytes
			fmt.Printf("OK   %s  removed=%d  freed=%d\n", res.ID, res.Removed, res.FreedBytes)
		}
		fmt.Printf("gc total removed=%d freed_bytes=%d\n", removed, freed)
		return nil
	}
	sess, err := resolveSessionByIDPrefix(mgr, idPrefix)
	if err != nil {
		return err
	}
	res, err := sess.GCBlobs()
	if err != nil {
		return err
	}
	fmt.Printf("gc %s  removed=%d  freed=%d  referenced=%d\n",
		res.ID, res.Removed, res.FreedBytes, res.Referenced)
	return nil
}

func runSessionExport(idPrefix, outPath string, includeTools, includeSystem bool) error {
	a, err := openSessionAgent()
	if err != nil {
		return err
	}
	defer a.Close()
	sess, err := resolveSessionByIDPrefix(a.Sessions(), idPrefix)
	if err != nil {
		return err
	}
	opts := session.ExportOptions{
		IncludeTools:  includeTools,
		IncludeSystem: includeSystem,
	}
	w := os.Stdout
	if strings.TrimSpace(outPath) != "" {
		if err := os.MkdirAll(filepath.Dir(outPath), 0700); err != nil && filepath.Dir(outPath) != "." {
			return err
		}
		f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	if err := sess.ExportMarkdown(w, opts); err != nil {
		return err
	}
	if strings.TrimSpace(outPath) != "" {
		fmt.Fprintf(os.Stderr, "exported %s -> %s\n", sess.ID, outPath)
	}
	return nil
}

func runSessionRoll(idPrefix string, retainTurns int, force bool) error {
	a, err := openSessionAgent()
	if err != nil {
		return err
	}
	defer a.Close()
	sess, err := resolveSessionByIDPrefix(a.Sessions(), idPrefix)
	if err != nil {
		return err
	}
	cfg := a.Config().Get().Context
	if !force && !sess.NeedsRoll(cfg.SessionMaxMessages, cfg.SessionMaxBytes) {
		fmt.Printf("session %s does not exceed roll limits (msgs=%d bytes=%d); pass --force to roll anyway\n",
			sess.ID, sess.MessageCount(), sess.ByteSize())
		return nil
	}
	if retainTurns <= 0 {
		retainTurns = cfg.SessionRollRetainTurns
	}
	res, err := sess.Roll(retainTurns)
	if err != nil {
		return err
	}
	if !res.Rolled {
		fmt.Printf("session %s: nothing to roll\n", sess.ID)
		return nil
	}
	fmt.Printf("rolled %s  dropped=%d  retained=%d  archive=%s\n",
		sess.ID, res.Dropped, res.Retained, res.ArchivePath)
	return nil
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
