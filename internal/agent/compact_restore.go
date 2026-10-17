package agent

import (
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

func buildPostCompactAttachments(sess *session.Session, messages []provider.Message, est *contextx.TokenEstimator) []session.CompactAttachment {
	var shell compactlib.ShellState
	if sess != nil {
		shell = sess
	}
	return compactlib.RestoreAttachments(shell, toCompactMessages(messages), compactEstimator(est), compactTextOptions())
}
