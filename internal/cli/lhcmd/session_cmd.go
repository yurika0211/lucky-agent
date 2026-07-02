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

	sessionCmd.AddCommand(compactCmd)
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
	fmt.Printf("dropped messages: %d\n", result.DroppedMessages)
	fmt.Printf("restored attachments: %d\n", result.RestoredAttachments)
	fmt.Printf("summary source: %s\n", result.SummarySource)
	fmt.Printf("summary tokens: %d\n", result.SummaryTokens)
	fmt.Printf("tokens: %d -> %d\n", result.PreTokenEstimate, result.PostTokenEstimate)
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
