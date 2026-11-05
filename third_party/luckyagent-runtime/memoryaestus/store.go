// Package memoryaestus is the only production integration with the Aestus SDK.
package memoryaestus

import (
	"context"
	"strings"
	"time"

	sdk "github.com/yurika0211/aestus/memory"
	"github.com/yurika0211/luckyagent-runtime/utils"
	"github.com/yurika0211/luckyagent-tools/memory"
)

type Store struct{ inner *sdk.Store }

var _ memory.Store = (*Store)(nil)
var _ memory.RuntimeStore = (*Store)(nil)

func (s *Store) Source() memory.SourceInfo {
	return memory.SourceInfo{Kind: "markdown", Location: s.inner.Dir()}
}

func (s *Store) CompactShortTerm(ctx context.Context, keepRecent, maxRunes int) (int, error) {
	return s.inner.CompactShortTerm(ctx, sdk.CompactionOptions{KeepRecent: keepRecent, MaxSummaryRunes: maxRunes})
}

func NewStore(dir string) (*Store, error) {
	inner, err := sdk.NewStore(dir)
	if err != nil {
		return nil, err
	}
	return &Store{inner: inner}, nil
}

func (s *Store) Remember(ctx context.Context, r memory.RememberRequest) (memory.RememberResult, error) {
	result, err := s.inner.Remember(ctx, sdk.RememberRequest{
		Content: r.Content, Category: r.Category, Tier: sdk.Tier(r.Tier), Importance: r.Importance,
		Options: toSDKSaveOptions(r.Options), Mode: sdk.WriteMode(r.Mode), EntryFilter: toSDKEntryFilter(r.EntryFilter),
	})
	return memory.RememberResult{SaveResult: fromSDKSaveResult(result.SaveResult), Superseded: append([]string(nil), result.Superseded...)}, err
}
func (s *Store) Recall(ctx context.Context, r memory.RecallRequest) ([]memory.SearchResult, error) {
	result, err := s.inner.Recall(ctx, sdk.RecallRequest{Query: r.Query, Options: toSDKSearchOptions(r.Options)})
	return mapSlice(result, fromSDKSearchResult), err
}
func (s *Store) AnalyzeMemoryContent(content string) []memory.HygieneIssue {
	return mapSlice(sdk.AnalyzeMemoryContent(content), fromSDKHygieneIssue)
}

func (s *Store) Activate(query string, opts memory.ActivationOptions) []memory.ActivationScore {
	r0 := s.inner.Activate(query, toSDKActivationOptions(opts))
	return mapSlice(r0, func(v sdk.ActivationScore) memory.ActivationScore { return fromSDKActivationScore(v) })
}

func (s *Store) ActiveStateIDs(stateKey string) []string {
	r0 := s.inner.ActiveStateIDs(stateKey)
	return mapSlice(r0, func(v string) string { return v })
}

func (s *Store) AuditHygiene(opts memory.HygieneOptions) memory.HygieneReport {
	r0 := s.inner.AuditHygiene(toSDKHygieneOptions(opts))
	return fromSDKHygieneReport(r0)
}

func (s *Store) BacklinksFor(id string) []memory.BacklinkContext {
	r0 := s.inner.BacklinksFor(id)
	return mapSlice(r0, func(v sdk.BacklinkContext) memory.BacklinkContext { return fromSDKBacklinkContext(v) })
}

func (s *Store) BuildSearchTrace(query string, mode string, source string, opts memory.SearchOptions, results []memory.SearchResult, duration time.Duration) memory.SearchTrace {
	r0 := s.inner.BuildSearchTrace(query, mode, source, toSDKSearchOptions(opts), mapSlice(results, func(v memory.SearchResult) sdk.SearchResult { return toSDKSearchResult(v) }), duration)
	return fromSDKSearchTrace(r0)
}

func (s *Store) ByCategory(category string) []memory.Entry {
	r0 := s.inner.ByCategory(category)
	return mapSlice(r0, func(v sdk.Entry) memory.Entry { return fromSDKEntry(v) })
}

func (s *Store) ByTier(tier memory.Tier) []memory.Entry {
	r0 := s.inner.ByTier(toSDKTier(tier))
	return mapSlice(r0, func(v sdk.Entry) memory.Entry { return fromSDKEntry(v) })
}

func (s *Store) ByTierWithOptions(tier memory.Tier, opts memory.RouteOptions) []memory.Entry {
	return mapSlice(s.inner.ByTierWithOptions(toSDKTier(tier), toSDKRouteOptions(opts)), fromSDKEntry)
}

func (s *Store) Close() error {
	r0 := s.inner.Close()
	return r0
}

func (s *Store) Count() int {
	r0 := s.inner.Count()
	return r0
}

func (s *Store) Decay(threshold float64) int {
	r0 := s.inner.Decay(threshold)
	return r0
}

func (s *Store) Dedup() int {
	r0 := s.inner.Dedup()
	return r0
}

func (s *Store) Delete(id string) error {
	r0 := s.inner.Delete(id)
	return r0
}

func (s *Store) DeleteDirty(opts memory.HygieneOptions) (memory.HygieneReport, error) {
	r0, r1 := s.inner.DeleteDirty(toSDKHygieneOptions(opts))
	return fromSDKHygieneReport(r0), r1
}

func (s *Store) Dir() string {
	r0 := s.inner.Dir()
	return r0
}

func (s *Store) Expire() int {
	r0 := s.inner.Expire()
	return r0
}

func (s *Store) ForgetSession(sessionID string) {
	s.inner.ForgetSession(sessionID)
}

func (s *Store) Get(id string) (*memory.Entry, error) {
	r0, r1 := s.inner.Get(id)
	return mapPointer(r0, func(v sdk.Entry) memory.Entry { return fromSDKEntry(v) }), r1
}

func (s *Store) GraphTopology(opts memory.GraphTopologyOptions) memory.GraphTopology {
	r0 := s.inner.GraphTopology(toSDKGraphTopologyOptions(opts))
	return fromSDKGraphTopology(r0)
}

func (s *Store) MigrateGraphMemory(opts memory.GraphMigrationOptions) (memory.GraphMigrationReport, error) {
	r0, r1 := s.inner.MigrateGraphMemory(toSDKGraphMigrationOptions(opts))
	return fromSDKGraphMigrationReport(r0), r1
}

func (s *Store) Promote(id string) error {
	r0 := s.inner.Promote(id)
	return r0
}

func (s *Store) PurgeCategory(category string) int {
	r0 := s.inner.PurgeCategory(category)
	return r0
}

func (s *Store) QuarantineDirty(opts memory.HygieneOptions) (memory.HygieneReport, error) {
	r0, r1 := s.inner.QuarantineDirty(toSDKHygieneOptions(opts))
	return fromSDKHygieneReport(r0), r1
}

func (s *Store) Recent(n int) []memory.Entry {
	r0 := s.inner.Recent(n)
	return mapSlice(r0, func(v sdk.Entry) memory.Entry { return fromSDKEntry(v) })
}

func (s *Store) RecordActivationFeedback(query string, entries []memory.Entry, signal string, value float64, now time.Time) {
	s.inner.RecordActivationFeedback(query, mapSlice(entries, func(v memory.Entry) sdk.Entry { return toSDKEntry(v) }), signal, value, now)
}

func (s *Store) RecordTurn(sessionID string) memory.MaintenanceEvent {
	r0 := s.inner.RecordTurn(sessionID)
	return fromSDKMaintenanceEvent(r0)
}

func (s *Store) RenameNotes(opts memory.NoteRenameOptions) (memory.NoteRenameReport, error) {
	r0, r1 := s.inner.RenameNotes(toSDKNoteRenameOptions(opts))
	return fromSDKNoteRenameReport(r0), r1
}

func (s *Store) ResolveTemporal(query string, activeEntries []memory.Entry) memory.TemporalResolution {
	r0 := s.inner.ResolveTemporal(query, mapSlice(activeEntries, func(v memory.Entry) sdk.Entry { return toSDKEntry(v) }))
	return fromSDKTemporalResolution(r0)
}

func (s *Store) RestoreHygiene(opts memory.HygieneOptions) (memory.HygieneReport, error) {
	r0, r1 := s.inner.RestoreHygiene(toSDKHygieneOptions(opts))
	return fromSDKHygieneReport(r0), r1
}

func (s *Store) Route(query string) memory.RouteAnalysis {
	r0 := s.inner.Route(query)
	return fromSDKRouteAnalysis(r0)
}

func (s *Store) RouteWithOptions(query string, opts memory.RouteOptions) memory.RouteAnalysis {
	r0 := s.inner.RouteWithOptions(query, toSDKRouteOptions(opts))
	return fromSDKRouteAnalysis(r0)
}

func (s *Store) Save(content string, category string) error {
	r0 := s.inner.Save(content, category)
	return r0
}

func (s *Store) SaveLongTerm(content string, category string) error {
	r0 := s.inner.SaveLongTerm(content, category)
	return r0
}

func (s *Store) SaveShortTerm(content string, category string) error {
	r0 := s.inner.SaveShortTerm(content, category)
	return r0
}

func (s *Store) SaveShortTermTTL(content string, category string, ttl time.Duration) error {
	r0 := s.inner.SaveShortTermTTL(content, category, ttl)
	return r0
}

func (s *Store) SaveWithMetadata(content string, category string, tier memory.Tier, importance float64, tags []string, links []string, aliases []string) error {
	r0 := s.inner.SaveWithMetadata(content, category, toSDKTier(tier), importance, mapSlice(tags, func(v string) string { return v }), mapSlice(links, func(v string) string { return v }), mapSlice(aliases, func(v string) string { return v }))
	return r0
}

func (s *Store) SaveWithOptions(content string, category string, tier memory.Tier, importance float64, opts memory.SaveOptions) error {
	r0 := s.inner.SaveWithOptions(content, category, toSDKTier(tier), importance, toSDKSaveOptions(opts))
	return r0
}

func (s *Store) SaveWithOptionsResult(content string, category string, tier memory.Tier, importance float64, opts memory.SaveOptions) (memory.SaveResult, error) {
	r0, r1 := s.inner.SaveWithOptionsResult(content, category, toSDKTier(tier), importance, toSDKSaveOptions(opts))
	return fromSDKSaveResult(r0), r1
}

func (s *Store) SaveWithTier(content string, category string, tier memory.Tier, importance float64) error {
	r0 := s.inner.SaveWithTier(content, category, toSDKTier(tier), importance)
	return r0
}

func (s *Store) SaveWithTierAndTags(content string, category string, tier memory.Tier, importance float64, tags []string) error {
	r0 := s.inner.SaveWithTierAndTags(content, category, toSDKTier(tier), importance, mapSlice(tags, func(v string) string { return v }))
	return r0
}

func (s *Store) Search(query string) []memory.Entry {
	r0 := s.inner.Search(query)
	return mapSlice(r0, func(v sdk.Entry) memory.Entry { return fromSDKEntry(v) })
}

func (s *Store) SearchParallel(query string, limit int) []memory.Entry {
	r0 := s.inner.SearchParallel(query, limit)
	return mapSlice(r0, func(v sdk.Entry) memory.Entry { return fromSDKEntry(v) })
}

func (s *Store) SearchWithOptions(query string, opts memory.SearchOptions) []memory.SearchResult {
	r0 := s.inner.SearchWithOptions(query, toSDKSearchOptions(opts))
	return mapSlice(r0, func(v sdk.SearchResult) memory.SearchResult { return fromSDKSearchResult(v) })
}

func (s *Store) Stats() map[memory.Tier]int {
	r0 := s.inner.Stats()
	return mapMap(r0, func(v sdk.Tier) memory.Tier { return fromSDKTier(v) }, func(v int) int { return v })
}

func (s *Store) Summarize(ids []string, summary string, category string) error {
	r0 := s.inner.Summarize(mapSlice(ids, func(v string) string { return v }), summary, category)
	return r0
}

func (s *Store) SupersedeEntries(ids []string) ([]string, error) {
	r0, r1 := s.inner.SupersedeEntries(mapSlice(ids, func(v string) string { return v }))
	return mapSlice(r0, func(v string) string { return v }), r1
}

func (s *Store) UnlinkedMentions() []memory.UnlinkedMention {
	r0 := s.inner.UnlinkedMentions()
	return mapSlice(r0, func(v sdk.UnlinkedMention) memory.UnlinkedMention { return fromSDKUnlinkedMention(v) })
}

type ShortTermBuffer struct{ inner *sdk.ShortTermBuffer }

func wrapShortTermBuffer(v *sdk.ShortTermBuffer) memory.ShortTermBuffer {
	if v == nil {
		return nil
	}
	return &ShortTermBuffer{inner: v}
}

func (s *ShortTermBuffer) Add(role string, content string) {
	s.inner.Add(role, content)
}

func (s *ShortTermBuffer) Clear() {
	s.inner.Clear()
}

func (s *ShortTermBuffer) Compress() {
	s.inner.Compress()
}

func (s *ShortTermBuffer) GetContext() []memory.Message {
	r0 := s.inner.GetContext()
	return mapSlice(r0, func(v sdk.Message) memory.Message { return fromSDKMessage(v) })
}

func (s *ShortTermBuffer) MaxTurns() int {
	r0 := s.inner.MaxTurns()
	return r0
}

func (s *ShortTermBuffer) MessageCount() int {
	r0 := s.inner.MessageCount()
	return r0
}

func (s *ShortTermBuffer) String() string {
	r0 := s.inner.String()
	return r0
}

func (s *ShortTermBuffer) Summary() string {
	r0 := s.inner.Summary()
	return r0
}

type SessionShortTermStore struct{ inner *sdk.SessionShortTermStore }

func wrapSessionShortTermStore(v *sdk.SessionShortTermStore) memory.SessionShortTermStore {
	if v == nil {
		return nil
	}
	return &SessionShortTermStore{inner: v}
}

func (s *SessionShortTermStore) Add(sessionID string, role string, content string) {
	s.inner.Add(sessionID, role, content)
}

func (s *SessionShortTermStore) Buffer(sessionID string) memory.ShortTermBuffer {
	r0 := s.inner.Buffer(sessionID)
	return wrapShortTermBuffer(r0)
}

func (s *SessionShortTermStore) Clear(sessionID string) {
	s.inner.Clear(sessionID)
}

func (s *SessionShortTermStore) Get(sessionID string) (memory.ShortTermBuffer, bool) {
	r0, r1 := s.inner.Get(sessionID)
	return wrapShortTermBuffer(r0), r1
}

func (s *SessionShortTermStore) GetContext(sessionID string) []memory.Message {
	r0 := s.inner.GetContext(sessionID)
	return mapSlice(r0, func(v sdk.Message) memory.Message { return fromSDKMessage(v) })
}

func (s *SessionShortTermStore) SessionCount() int {
	r0 := s.inner.SessionCount()
	return r0
}

func (s *SessionShortTermStore) Summary(sessionID string) string {
	r0 := s.inner.Summary(sessionID)
	return r0
}

type MidTermStore struct{ inner *sdk.MidTermStore }

func wrapMidTermStore(v *sdk.MidTermStore) memory.MidTermStore {
	if v == nil {
		return nil
	}
	return &MidTermStore{inner: v}
}

func (s *MidTermStore) Count() int {
	r0 := s.inner.Count()
	return r0
}

func (s *MidTermStore) SaveSummary(ctx context.Context, r memory.SummaryRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	summary := GenerateSessionSummary(r.SessionID, r.UserID, r.Turns)
	if content := strings.TrimSpace(r.Content); content != "" {
		summary.RawSummary = content
	}
	if title := strings.TrimSpace(r.Title); title != "" {
		summary.Topics = append([]string{title}, summary.Topics...)
	}
	summary.Topics = utils.DedupStringsLimit(summary.Topics, 6)
	return s.SaveSessionSummary(summary)
}

func (s *MidTermStore) Delete(sessionID string) error {
	r0 := s.inner.Delete(sessionID)
	return r0
}

func (s *MidTermStore) ExpireOldSummaries(olderThan time.Duration) int {
	r0 := s.inner.ExpireOldSummaries(olderThan)
	return r0
}

func (s *MidTermStore) Get(sessionID string) (*memory.SessionSummary, bool) {
	r0, r1 := s.inner.Get(sessionID)
	return mapPointer(r0, func(v sdk.SessionSummary) memory.SessionSummary { return fromSDKSessionSummary(v) }), r1
}

func (s *MidTermStore) ListAll() []memory.SessionSummary {
	r0 := s.inner.ListAll()
	return mapSlice(r0, func(v sdk.SessionSummary) memory.SessionSummary { return fromSDKSessionSummary(v) })
}

func (s *MidTermStore) SaveSessionSummary(summary *memory.SessionSummary) error {
	r0 := s.inner.SaveSessionSummary(mapPointer(summary, func(v memory.SessionSummary) sdk.SessionSummary { return toSDKSessionSummary(v) }))
	return r0
}

func (s *MidTermStore) SearchSummaries(query string, topK int) []memory.SessionSummary {
	r0 := s.inner.SearchSummaries(query, topK)
	return mapSlice(r0, func(v sdk.SessionSummary) memory.SessionSummary { return fromSDKSessionSummary(v) })
}

func NewShortTermBuffer(maxTurns int) *ShortTermBuffer {
	return &ShortTermBuffer{inner: sdk.NewShortTermBuffer(maxTurns)}
}
func NewSessionShortTermStore(maxTurns int) *SessionShortTermStore {
	return &SessionShortTermStore{inner: sdk.NewSessionShortTermStore(maxTurns)}
}
func NewMidTermStore(dir string, maxSummaries int) (*MidTermStore, error) {
	inner, err := sdk.NewMidTermStore(dir, maxSummaries)
	if err != nil {
		return nil, err
	}
	return &MidTermStore{inner: inner}, nil
}
func GenerateSessionSummary(sessionID, userID string, messages []memory.ConversationTurn) *memory.SessionSummary {
	return mapPointer(sdk.GenerateSessionSummary(sessionID, userID, mapSlice(messages, toSDKConversationTurn)), fromSDKSessionSummary)
}
