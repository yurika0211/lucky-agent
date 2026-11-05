package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/utils"
)

func TestSessionIndexMatchesSidecarAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess := mgr.New()
	sess.SetTitle("indexed")
	sess.SetPinned(true)
	sess.SetProject("catalog")
	sess.AddMessage("user", "first")
	sess.AddMessage("assistant", "second")
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}

	rows, err := mgr.index.List()
	if err != nil {
		t.Fatal(err)
	}
	row, ok := rows[sess.ID]
	if !ok || row.Meta.Title != "indexed" || !row.Meta.Pinned || row.Meta.Project != "catalog" || row.Meta.MessageCount != 2 {
		t.Fatalf("index row = %+v", row.Meta)
	}
	if row.TailOffset < 0 || row.FileSize <= 0 || row.TailOffset >= row.FileSize {
		t.Fatalf("tail offset=%d file=%d", row.TailOffset, row.FileSize)
	}
	info, err := os.Stat(sessionMetadataPath(dir, sess.ID))
	if err != nil || row.SourceSize != info.Size() || !row.SourceTime.Equal(info.ModTime()) {
		t.Fatalf("fingerprint size=%d time=%v info=%v err=%v", row.SourceSize, row.SourceTime, info, err)
	}

	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := reloaded.Get(sess.ID)
	if !ok || loaded.messagesLoaded || loaded.Title != "indexed" || loaded.tailOffset != row.TailOffset {
		t.Fatalf("reload title=%q loaded=%v offset=%d", loaded.Title, loaded.messagesLoaded, loaded.tailOffset)
	}
	page, total, more, err := loaded.GetMessagesPage(1, 0)
	if err != nil || total != 2 || !more || len(page) != 1 || page[0].Content != "second" {
		t.Fatalf("tail page=%v total=%d more=%v err=%v loaded=%v tail=%d/%d/%d", page, total, more, err, loaded.messagesLoaded, loaded.tailOffset, loaded.tailStart, loaded.tailSize)
	}
	if loaded.pageIndex != nil {
		t.Fatalf("latest page rebuilt a full offset index loaded=%v tail=%d/%d/%d index=%v", loaded.messagesLoaded, loaded.tailOffset, loaded.tailStart, loaded.tailSize, loaded.pageIndex != nil)
	}
	page, total, _, err = loaded.GetMessagesPage(1, 1)
	if err != nil || total != 2 || len(page) != 1 || page[0].Content != "first" {
		t.Fatalf("older page=%v total=%d err=%v", page, total, err)
	}
}

func TestLongSessionTailSurvivesRestartAndReplacement(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess := mgr.New()
	const total = maxPageCacheMessages + 40
	for i := 0; i < total; i++ {
		sess.AddMessage("user", fmt.Sprintf("message-%05d", i))
	}
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	rows, err := mgr.index.List()
	if err != nil {
		t.Fatal(err)
	}
	row := rows[sess.ID]
	if row.TailStart != total-maxPageCacheMessages || row.TailOffset <= 0 || row.FileSize <= row.TailOffset {
		t.Fatalf("window start=%d offset=%d size=%d", row.TailStart, row.TailOffset, row.FileSize)
	}

	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := reloaded.Get(sess.ID)
	if !ok || loaded.tailStart != row.TailStart || loaded.tailOffset != row.TailOffset {
		t.Fatalf("reloaded start=%d offset=%d", loaded.tailStart, loaded.tailOffset)
	}
	page, count, more, err := loaded.GetMessagesPage(40, 0)
	if err != nil || count != total || !more || len(page) != 40 || page[0].Content != fmt.Sprintf("message-%05d", total-40) {
		t.Fatalf("page=%v count=%d more=%v err=%v", pageContents(page), count, more, err)
	}
	if loaded.pageIndex != nil {
		t.Fatalf("latest page scanned the file into an offset index loaded=%v tail=%d/%d/%d", loaded.messagesLoaded, loaded.tailOffset, loaded.tailStart, loaded.tailSize)
	}

	loaded.AddMessage("assistant", "message-extra")
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	if loaded.tailStart != row.TailStart+1 || loaded.tailOffset <= row.TailOffset {
		t.Fatalf("window did not advance: start=%d offset=%d old=%d", loaded.tailStart, loaded.tailOffset, row.TailOffset)
	}
	page, _, _, err = loaded.GetMessagesPage(1, 0)
	if err != nil || len(page) != 1 || page[0].Content != "message-extra" {
		t.Fatalf("appended page=%v err=%v", pageContents(page), err)
	}

	replaced, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	cold, ok := replaced.Get(sess.ID)
	if !ok {
		t.Fatal("missing replaced session")
	}
	path := cold.messagesJSONLPath()
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"replaced\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	page, count, _, err = cold.GetMessagesPage(40, 0)
	if err != nil || count != 1 || len(page) != 1 || page[0].Content != "replaced" || cold.pageIndex == nil || cold.tailSize != 0 {
		t.Fatalf("replaced page=%v count=%d index=%v tail=%d err=%v", pageContents(page), count, cold.pageIndex != nil, cold.tailSize, err)
	}
}

func pageContents(page []provider.Message) []string {
	out := make([]string, len(page))
	for i := range page {
		out[i] = page[i].Content
	}
	return out
}

func TestSessionIndexCorrectsExternalMetadataAndDelete(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess := mgr.NewWithTitle("before")
	sess.AddMessage("user", "kept")
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	metaPath := sessionMetadataPath(dir, sess.ID)
	meta, ok := readSessionMetadataLoose(dir, sess.ID)
	if !ok {
		t.Fatal("missing sidecar")
	}
	meta.Title = "after external edit"
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := utils.WriteFileAtomic(metaPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(metaPath, later, later); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := reloaded.Get(sess.ID)
	if !ok || loaded.Title != "after external edit" {
		t.Fatalf("stale index title=%q", loaded.Title)
	}
	rows, err := reloaded.index.List()
	if err != nil || rows[sess.ID].Meta.Title != "after external edit" {
		t.Fatalf("index was not corrected: %+v err=%v", rows[sess.ID].Meta, err)
	}

	legacy := NewSession("legacy-only", dir)
	legacy.format = FormatLegacyMD
	legacy.Title = "old file"
	legacy.AddMessage("user", "legacy body")
	if err := legacy.saveLegacyMarkdownWithProfile(dir, legacy.ID, legacy.Title, "", "", "", false, legacy.CreatedAt, legacy.UpdatedAt, ShellContext{}, legacy.Messages); err != nil {
		t.Fatal(err)
	}
	withLegacy, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := withLegacy.Get(legacy.ID); !ok || got.Title != "old file" || got.Format() != FormatLegacyMD {
		t.Fatalf("legacy session missing: %+v", got)
	}
	rows, err = withLegacy.index.List()
	if err != nil || rows[legacy.ID].Meta.Format != FormatLegacyMD {
		t.Fatalf("legacy row=%+v err=%v", rows[legacy.ID].Meta, err)
	}

	if err := withLegacy.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := withLegacy.Get(sess.ID); ok {
		t.Fatal("deleted session remained listed")
	}
	rows, err = withLegacy.index.List()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows[sess.ID]; ok {
		t.Fatal("deleted session remained in the index")
	}
	if _, err := os.Stat(filepath.Join(dir, sessionIndexName)); err != nil {
		t.Fatal(err)
	}
}

func TestSessionIndexFallsBackWhenDatabaseIsUnreadable(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess := mgr.NewWithTitle("survives")
	sess.AddMessage("user", "body")
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionIndexName), []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := reloaded.Get(sess.ID)
	if !ok || loaded.Title != "survives" {
		t.Fatalf("corrupt index hid the session: %+v", loaded)
	}
	page, total, _, err := loaded.GetMessagesPage(10, 0)
	if err != nil || total != 1 || page[0].Content != "body" {
		t.Fatalf("page=%v total=%d err=%v", page, total, err)
	}
}
