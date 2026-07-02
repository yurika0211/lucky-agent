package lhcmd

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/session"
)

func TestResolveSessionByIDPrefix(t *testing.T) {
	mgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("new session manager: %v", err)
	}
	mgr.Ensure("alpha-one")
	mgr.Ensure("beta-one")

	sess, err := resolveSessionByIDPrefix(mgr, "alpha")
	if err != nil {
		t.Fatalf("resolve unique prefix: %v", err)
	}
	if sess.ID != "alpha-one" {
		t.Fatalf("expected alpha-one, got %s", sess.ID)
	}

	if _, err := resolveSessionByIDPrefix(mgr, "missing"); err == nil {
		t.Fatal("expected missing prefix to fail")
	}
}

func TestResolveSessionByIDPrefixRejectsAmbiguousPrefix(t *testing.T) {
	mgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("new session manager: %v", err)
	}
	mgr.Ensure("alpha-one")
	mgr.Ensure("alpha-two")

	_, err = resolveSessionByIDPrefix(mgr, "alpha")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous prefix error, got %v", err)
	}
}
