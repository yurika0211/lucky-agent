package memoryaestus

import (
	"time"

	sdk "github.com/yurika0211/aestus/memory"
	"github.com/yurika0211/aestus/memory/policy"
	"github.com/yurika0211/luckyagent-tools/memory"
	"github.com/yurika0211/luckyagent-tools/memory/diagnostic"
)

func fromSDKActivationComponents(v sdk.ActivationComponents) memory.ActivationComponents {
	return memory.ActivationComponents{
		Lexical:    v.Lexical,
		Category:   v.Category,
		Tags:       v.Tags,
		Aliases:    v.Aliases,
		Links:      v.Links,
		Importance: v.Importance,
		Tier:       v.Tier,
		Recency:    v.Recency,
		Access:     v.Access,
		GraphBoost: v.GraphBoost,
		TidalBoost: v.TidalBoost,
	}
}

func toSDKActivationComponents(v memory.ActivationComponents) sdk.ActivationComponents {
	return sdk.ActivationComponents{
		Lexical:    v.Lexical,
		Category:   v.Category,
		Tags:       v.Tags,
		Aliases:    v.Aliases,
		Links:      v.Links,
		Importance: v.Importance,
		Tier:       v.Tier,
		Recency:    v.Recency,
		Access:     v.Access,
		GraphBoost: v.GraphBoost,
		TidalBoost: v.TidalBoost,
	}
}

func fromSDKActivationExcerpt(v sdk.ActivationExcerpt) memory.ActivationExcerpt {
	return memory.ActivationExcerpt{
		Heading: v.Heading,
		Block:   v.Block,
		Context: v.Context,
		Missing: v.Missing,
	}
}

func toSDKActivationExcerpt(v memory.ActivationExcerpt) sdk.ActivationExcerpt {
	return sdk.ActivationExcerpt{
		Heading: v.Heading,
		Block:   v.Block,
		Context: v.Context,
		Missing: v.Missing,
	}
}

func fromSDKActivationFeedback(v sdk.ActivationFeedback) memory.ActivationFeedback {
	return memory.ActivationFeedback{
		Query:   v.Query,
		QueryID: v.QueryID,
		Entry:   fromSDKEntry(v.Entry),
		Signal:  v.Signal,
		Value:   v.Value,
		At:      v.At,
		Keys:    mapSlice(v.Keys, func(v string) string { return v }),
	}
}

func toSDKActivationFeedback(v memory.ActivationFeedback) sdk.ActivationFeedback {
	return sdk.ActivationFeedback{
		Query:   v.Query,
		QueryID: v.QueryID,
		Entry:   toSDKEntry(v.Entry),
		Signal:  v.Signal,
		Value:   v.Value,
		At:      v.At,
		Keys:    mapSlice(v.Keys, func(v string) string { return v }),
	}
}

func toSDKActivationOptions(v memory.ActivationOptions) sdk.ActivationOptions {
	return sdk.ActivationOptions{
		EntryFilter:       toSDKEntryFilter(v.EntryFilter),
		Limit:             v.Limit,
		IncludeGraph:      v.IncludeGraph,
		MaxGraphDepth:     v.MaxGraphDepth,
		MaxGraphBoost:     v.MaxGraphBoost,
		MaxGraphSeeds:     v.MaxGraphSeeds,
		UpdateAccessStats: v.UpdateAccessStats,
		Explain:           v.Explain,
	}
}

func fromSDKActivationPath(v sdk.ActivationPath) memory.ActivationPath {
	return memory.ActivationPath{
		FromID:  v.FromID,
		ToID:    v.ToID,
		Via:     v.Via,
		Kind:    v.Kind,
		Heading: v.Heading,
		Block:   v.Block,
		Weight:  v.Weight,
		Depth:   v.Depth,
		Boost:   v.Boost,
	}
}

func toSDKActivationPath(v memory.ActivationPath) sdk.ActivationPath {
	return sdk.ActivationPath{
		FromID:  v.FromID,
		ToID:    v.ToID,
		Via:     v.Via,
		Kind:    v.Kind,
		Heading: v.Heading,
		Block:   v.Block,
		Weight:  v.Weight,
		Depth:   v.Depth,
		Boost:   v.Boost,
	}
}

func fromSDKActivationScore(v sdk.ActivationScore) memory.ActivationScore {
	return memory.ActivationScore{
		EntryID:     v.EntryID,
		Entry:       fromSDKEntry(v.Entry),
		Score:       v.Score,
		Components:  fromSDKActivationComponents(v.Components),
		Paths:       mapSlice(v.Paths, func(v sdk.ActivationPath) memory.ActivationPath { return fromSDKActivationPath(v) }),
		Excerpts:    mapSlice(v.Excerpts, func(v sdk.ActivationExcerpt) memory.ActivationExcerpt { return fromSDKActivationExcerpt(v) }),
		DirectScore: v.DirectScore,
	}
}

func toSDKActivationScore(v memory.ActivationScore) sdk.ActivationScore {
	return sdk.ActivationScore{
		EntryID:     v.EntryID,
		Entry:       toSDKEntry(v.Entry),
		Score:       v.Score,
		Components:  toSDKActivationComponents(v.Components),
		Paths:       mapSlice(v.Paths, func(v memory.ActivationPath) sdk.ActivationPath { return toSDKActivationPath(v) }),
		Excerpts:    mapSlice(v.Excerpts, func(v memory.ActivationExcerpt) sdk.ActivationExcerpt { return toSDKActivationExcerpt(v) }),
		DirectScore: v.DirectScore,
	}
}

func fromSDKAppliedRoutePolicy(v sdk.AppliedRoutePolicy) memory.AppliedRoutePolicy {
	return memory.AppliedRoutePolicy{
		ID:          v.ID,
		EntryID:     v.EntryID,
		EvidenceRef: v.EvidenceRef,
	}
}

func fromSDKBacklinkContext(v sdk.BacklinkContext) memory.BacklinkContext {
	return memory.BacklinkContext{
		SourceID: v.SourceID,
		Source:   v.Source,
		Target:   v.Target,
		Heading:  v.Heading,
		Block:    v.Block,
		Display:  v.Display,
		Embed:    v.Embed,
		Line:     v.Line,
		Excerpt:  v.Excerpt,
	}
}

func toSDKConversationTurn(v memory.ConversationTurn) sdk.ConversationTurn {
	return sdk.ConversationTurn{
		Role:    v.Role,
		Content: v.Content,
	}
}

func fromSDKEntry(v sdk.Entry) memory.Entry {
	return memory.Entry{
		ID:            v.ID,
		Content:       v.Content,
		Category:      v.Category,
		Tier:          fromSDKTier(v.Tier),
		Importance:    v.Importance,
		AccessCount:   v.AccessCount,
		CreatedAt:     v.CreatedAt,
		AccessedAt:    v.AccessedAt,
		Tags:          mapSlice(v.Tags, func(v string) string { return v }),
		SummaryOf:     mapSlice(v.SummaryOf, func(v string) string { return v }),
		ExpiresAt:     mapPointer(v.ExpiresAt, func(v time.Time) time.Time { return v }),
		Status:        v.Status,
		ValidFrom:     v.ValidFrom,
		ValidUntil:    mapPointer(v.ValidUntil, func(v time.Time) time.Time { return v }),
		Links:         mapSlice(v.Links, func(v string) string { return v }),
		Aliases:       mapSlice(v.Aliases, func(v string) string { return v }),
		StateKey:      v.StateKey,
		StateValue:    v.StateValue,
		Confidence:    v.Confidence,
		Supersedes:    mapSlice(v.Supersedes, func(v string) string { return v }),
		RoutePolicies: mapSlice(v.RoutePolicies, func(v sdk.RoutePolicy) memory.RoutePolicy { return fromSDKRoutePolicy(v) }),
		BlockID:       v.BlockID,
		Path:          v.Path,
	}
}

func toSDKEntry(v memory.Entry) sdk.Entry {
	return sdk.Entry{
		ID:            v.ID,
		Content:       v.Content,
		Category:      v.Category,
		Tier:          toSDKTier(v.Tier),
		Importance:    v.Importance,
		AccessCount:   v.AccessCount,
		CreatedAt:     v.CreatedAt,
		AccessedAt:    v.AccessedAt,
		Tags:          mapSlice(v.Tags, func(v string) string { return v }),
		SummaryOf:     mapSlice(v.SummaryOf, func(v string) string { return v }),
		ExpiresAt:     mapPointer(v.ExpiresAt, func(v time.Time) time.Time { return v }),
		Status:        v.Status,
		ValidFrom:     v.ValidFrom,
		ValidUntil:    mapPointer(v.ValidUntil, func(v time.Time) time.Time { return v }),
		Links:         mapSlice(v.Links, func(v string) string { return v }),
		Aliases:       mapSlice(v.Aliases, func(v string) string { return v }),
		StateKey:      v.StateKey,
		StateValue:    v.StateValue,
		Confidence:    v.Confidence,
		Supersedes:    mapSlice(v.Supersedes, func(v string) string { return v }),
		RoutePolicies: mapSlice(v.RoutePolicies, func(v memory.RoutePolicy) sdk.RoutePolicy { return toSDKRoutePolicy(v) }),
		BlockID:       v.BlockID,
		Path:          v.Path,
	}
}

func fromSDKGraphLinkRef(v sdk.GraphLinkRef) memory.GraphLinkRef {
	return memory.GraphLinkRef{
		Heading:    v.Heading,
		Block:      v.Block,
		Display:    v.Display,
		Embed:      v.Embed,
		Ambiguous:  v.Ambiguous,
		Candidates: mapSlice(v.Candidates, func(v string) string { return v }),
	}
}

func fromSDKGraphMigrationEntryPlan(v sdk.GraphMigrationEntryPlan) memory.GraphMigrationEntryPlan {
	return memory.GraphMigrationEntryPlan{
		ID:          v.ID,
		Path:        v.Path,
		Category:    v.Category,
		Tier:        v.Tier,
		AddLinks:    mapSlice(v.AddLinks, func(v string) string { return v }),
		AddAliases:  mapSlice(v.AddAliases, func(v string) string { return v }),
		AddTags:     mapSlice(v.AddTags, func(v string) string { return v }),
		Archive:     v.Archive,
		ArchivePath: v.ArchivePath,
		Reason:      v.Reason,
		Preview:     v.Preview,
	}
}

func toSDKGraphMigrationOptions(v memory.GraphMigrationOptions) sdk.GraphMigrationOptions {
	return sdk.GraphMigrationOptions{
		Apply:            v.Apply,
		ArchiveDirty:     v.ArchiveDirty,
		MaxDirtyFindings: v.MaxDirtyFindings,
		Now:              v.Now,
	}
}

func fromSDKGraphMigrationReport(v sdk.GraphMigrationReport) memory.GraphMigrationReport {
	return memory.GraphMigrationReport{
		Scanned:          v.Scanned,
		WouldUpdateLinks: v.WouldUpdateLinks,
		UpdatedLinks:     v.UpdatedLinks,
		WouldArchive:     v.WouldArchive,
		Archived:         v.Archived,
		DirtyFindings:    mapSlice(v.DirtyFindings, func(v sdk.HygieneIssue) memory.HygieneIssue { return fromSDKHygieneIssue(v) }),
		Entries: mapSlice(v.Entries, func(v sdk.GraphMigrationEntryPlan) memory.GraphMigrationEntryPlan {
			return fromSDKGraphMigrationEntryPlan(v)
		}),
		Apply: v.Apply,
	}
}

func fromSDKGraphTopology(v sdk.GraphTopology) memory.GraphTopology {
	return memory.GraphTopology{
		Nodes:         mapSlice(v.Nodes, func(v sdk.GraphTopologyNode) memory.GraphTopologyNode { return fromSDKGraphTopologyNode(v) }),
		Edges:         mapSlice(v.Edges, func(v sdk.GraphTopologyEdge) memory.GraphTopologyEdge { return fromSDKGraphTopologyEdge(v) }),
		TotalNotes:    v.TotalNotes,
		TotalEdges:    v.TotalEdges,
		IsolatedCount: v.IsolatedCount,
		Unresolved:    v.Unresolved,
		Truncated:     v.Truncated,
		Categories:    mapSlice(v.Categories, func(v string) string { return v }),
	}
}

func fromSDKGraphTopologyEdge(v sdk.GraphTopologyEdge) memory.GraphTopologyEdge {
	return memory.GraphTopologyEdge{
		Source: v.Source,
		Target: v.Target,
		Weight: v.Weight,
		Refs:   mapSlice(v.Refs, func(v sdk.GraphLinkRef) memory.GraphLinkRef { return fromSDKGraphLinkRef(v) }),
	}
}

func fromSDKGraphTopologyNode(v sdk.GraphTopologyNode) memory.GraphTopologyNode {
	return memory.GraphTopologyNode{
		ID:         v.ID,
		Title:      v.Title,
		Category:   v.Category,
		Tier:       v.Tier,
		Path:       v.Path,
		Tags:       mapSlice(v.Tags, func(v string) string { return v }),
		Importance: v.Importance,
		Degree:     v.Degree,
		Resolved:   v.Resolved,
		Ambiguous:  v.Ambiguous,
		Candidates: mapSlice(v.Candidates, func(v string) string { return v }),
	}
}

func toSDKGraphTopologyOptions(v memory.GraphTopologyOptions) sdk.GraphTopologyOptions {
	return sdk.GraphTopologyOptions{
		Limit:           v.Limit,
		IncludeIsolated: v.IncludeIsolated,
	}
}

func fromSDKHygieneIssue(v sdk.HygieneIssue) memory.HygieneIssue {
	return memory.HygieneIssue{
		ID:              v.ID,
		Path:            v.Path,
		Category:        v.Category,
		Tier:            v.Tier,
		Severity:        v.Severity,
		Reason:          v.Reason,
		SuggestedAction: v.SuggestedAction,
		Score:           v.Score,
		Preview:         v.Preview,
	}
}

func toSDKHygieneOptions(v memory.HygieneOptions) sdk.HygieneOptions {
	return sdk.HygieneOptions{
		IncludeInactive: v.IncludeInactive,
		MinSeverity:     v.MinSeverity,
		MaxFindings:     v.MaxFindings,
		Now:             v.Now,
		IDs:             mapSlice(v.IDs, func(v string) string { return v }),
	}
}

func fromSDKHygieneReport(v sdk.HygieneReport) memory.HygieneReport {
	return memory.HygieneReport{
		Scanned:     v.Scanned,
		Findings:    mapSlice(v.Findings, func(v sdk.HygieneIssue) memory.HygieneIssue { return fromSDKHygieneIssue(v) }),
		Quarantined: v.Quarantined,
		Deleted:     v.Deleted,
		Restored:    v.Restored,
		DryRun:      v.DryRun,
		Action:      v.Action,
	}
}

func fromSDKMaintenanceEvent(v sdk.MaintenanceEvent) memory.MaintenanceEvent {
	return memory.MaintenanceEvent{
		SessionID:    v.SessionID,
		RuntimeCount: v.RuntimeCount,
		SessionCount: v.SessionCount,
		RunDecay:     v.RunDecay,
		RunSummarize: v.RunSummarize,
		RunExpire:    v.RunExpire,
	}
}

func fromSDKMessage(v sdk.Message) memory.Message {
	return memory.Message{
		Role:    v.Role,
		Content: v.Content,
	}
}

func fromSDKNoteDuplicatePlan(v sdk.NoteDuplicatePlan) memory.NoteDuplicatePlan {
	return memory.NoteDuplicatePlan{
		ID:     v.ID,
		Keep:   v.Keep,
		Remove: v.Remove,
		Backup: v.Backup,
	}
}

func toSDKNoteRenameOptions(v memory.NoteRenameOptions) sdk.NoteRenameOptions {
	return sdk.NoteRenameOptions{
		Apply: v.Apply,
		Limit: v.Limit,
	}
}

func fromSDKNoteRenamePlan(v sdk.NoteRenamePlan) memory.NoteRenamePlan {
	return memory.NoteRenamePlan{
		ID:       v.ID,
		From:     v.From,
		To:       v.To,
		Category: v.Category,
		Tier:     v.Tier,
		Preview:  v.Preview,
	}
}

func fromSDKNoteRenameReport(v sdk.NoteRenameReport) memory.NoteRenameReport {
	return memory.NoteRenameReport{
		Scanned:               v.Scanned,
		WouldRename:           v.WouldRename,
		Renamed:               v.Renamed,
		WouldPruneDuplicates:  v.WouldPruneDuplicates,
		PrunedDuplicates:      v.PrunedDuplicates,
		Entries:               mapSlice(v.Entries, func(v sdk.NoteRenamePlan) memory.NoteRenamePlan { return fromSDKNoteRenamePlan(v) }),
		DuplicatePruneEntries: mapSlice(v.DuplicatePruneEntries, func(v sdk.NoteDuplicatePlan) memory.NoteDuplicatePlan { return fromSDKNoteDuplicatePlan(v) }),
		Apply:                 v.Apply,
	}
}

func fromSDKRouteAnalysis(v sdk.RouteAnalysis) memory.RouteAnalysis {
	return memory.RouteAnalysis{
		Query:   v.Query,
		Entries: mapSlice(v.Entries, func(v sdk.Entry) memory.Entry { return fromSDKEntry(v) }),
		Excerpts: mapMap(v.Excerpts, func(k string) string { return k }, func(items []policy.Excerpt) []memory.ActivationExcerpt {
			return mapSlice(items, func(v policy.Excerpt) memory.ActivationExcerpt { return memory.ActivationExcerpt(v) })
		}),
		ToolRequirements:  mapSlice(v.ToolRequirements, func(v sdk.RouteToolRequirement) memory.RouteToolRequirement { return fromSDKRouteToolRequirement(v) }),
		Risks:             mapSlice(v.Risks, func(v sdk.RouteRisk) memory.RouteRisk { return fromSDKRouteRisk(v) }),
		AppliedPolicies:   mapSlice(v.AppliedPolicies, func(v sdk.AppliedRoutePolicy) memory.AppliedRoutePolicy { return fromSDKAppliedRoutePolicy(v) }),
		RequiredTools:     mapSlice(v.RequiredTools, func(v string) string { return v }),
		SuggestedSearches: mapSlice(v.SuggestedSearches, func(v string) string { return v }),
		RiskFlags:         mapSlice(v.RiskFlags, func(v string) string { return v }),
		Constraints:       mapSlice(v.Constraints, func(v string) string { return v }),
		Clarifications:    mapSlice(v.Clarifications, func(v string) string { return v }),
		TemporalNotes:     mapSlice(v.TemporalNotes, func(v string) string { return v }),
		EvidenceRefs:      mapSlice(v.EvidenceRefs, func(v string) string { return v }),
		SupersededRefs:    mapSlice(v.SupersededRefs, func(v string) string { return v }),
		ConflictRefs:      mapSlice(v.ConflictRefs, func(v string) string { return v }),
		ExpiredRefs:       mapSlice(v.ExpiredRefs, func(v string) string { return v }),
		FutureRefs:        mapSlice(v.FutureRefs, func(v string) string { return v }),
	}
}

func toSDKRouteOptions(v memory.RouteOptions) sdk.RouteOptions {
	return sdk.RouteOptions{
		EntryFilter: toSDKEntryFilter(v.EntryFilter),
	}
}

func fromSDKRoutePolicy(v sdk.RoutePolicy) memory.RoutePolicy {
	return memory.RoutePolicy{
		ID:             v.ID,
		Match:          fromSDKRoutePolicyMatch(v.Match),
		Risks:          mapSlice(v.Risks, func(v sdk.RouteRisk) memory.RouteRisk { return fromSDKRouteRisk(v) }),
		RequiredTools:  mapSlice(v.RequiredTools, func(v sdk.RouteToolRequirement) memory.RouteToolRequirement { return fromSDKRouteToolRequirement(v) }),
		Constraints:    mapSlice(v.Constraints, func(v string) string { return v }),
		Clarifications: mapSlice(v.Clarifications, func(v string) string { return v }),
	}
}

func toSDKRoutePolicy(v memory.RoutePolicy) sdk.RoutePolicy {
	return sdk.RoutePolicy{
		ID:             v.ID,
		Match:          toSDKRoutePolicyMatch(v.Match),
		Risks:          mapSlice(v.Risks, func(v memory.RouteRisk) sdk.RouteRisk { return toSDKRouteRisk(v) }),
		RequiredTools:  mapSlice(v.RequiredTools, func(v memory.RouteToolRequirement) sdk.RouteToolRequirement { return toSDKRouteToolRequirement(v) }),
		Constraints:    mapSlice(v.Constraints, func(v string) string { return v }),
		Clarifications: mapSlice(v.Clarifications, func(v string) string { return v }),
	}
}

func fromSDKRoutePolicyMatch(v sdk.RoutePolicyMatch) memory.RoutePolicyMatch {
	return memory.RoutePolicyMatch{
		QueryAll:  mapSlice(v.QueryAll, func(v sdk.RouteTermGroup) memory.RouteTermGroup { return fromSDKRouteTermGroup(v) }),
		QueryAny:  mapSlice(v.QueryAny, func(v string) string { return v }),
		QueryNone: mapSlice(v.QueryNone, func(v string) string { return v }),
		States:    mapSlice(v.States, func(v sdk.RouteStateMatch) memory.RouteStateMatch { return fromSDKRouteStateMatch(v) }),
	}
}

func toSDKRoutePolicyMatch(v memory.RoutePolicyMatch) sdk.RoutePolicyMatch {
	return sdk.RoutePolicyMatch{
		QueryAll:  mapSlice(v.QueryAll, func(v memory.RouteTermGroup) sdk.RouteTermGroup { return toSDKRouteTermGroup(v) }),
		QueryAny:  mapSlice(v.QueryAny, func(v string) string { return v }),
		QueryNone: mapSlice(v.QueryNone, func(v string) string { return v }),
		States:    mapSlice(v.States, func(v memory.RouteStateMatch) sdk.RouteStateMatch { return toSDKRouteStateMatch(v) }),
	}
}

func fromSDKRouteRisk(v sdk.RouteRisk) memory.RouteRisk {
	return memory.RouteRisk{
		Name:     v.Name,
		Priority: v.Priority,
	}
}

func toSDKRouteRisk(v memory.RouteRisk) sdk.RouteRisk {
	return sdk.RouteRisk{
		Name:     v.Name,
		Priority: v.Priority,
	}
}

func fromSDKRouteStateMatch(v sdk.RouteStateMatch) memory.RouteStateMatch {
	return memory.RouteStateMatch{
		Key:       v.Key,
		Values:    mapSlice(v.Values, func(v string) string { return v }),
		NotValues: mapSlice(v.NotValues, func(v string) string { return v }),
	}
}

func toSDKRouteStateMatch(v memory.RouteStateMatch) sdk.RouteStateMatch {
	return sdk.RouteStateMatch{
		Key:       v.Key,
		Values:    mapSlice(v.Values, func(v string) string { return v }),
		NotValues: mapSlice(v.NotValues, func(v string) string { return v }),
	}
}

func fromSDKRouteTermGroup(v sdk.RouteTermGroup) memory.RouteTermGroup {
	return memory.RouteTermGroup{
		Any: mapSlice(v.Any, func(v string) string { return v }),
	}
}

func toSDKRouteTermGroup(v memory.RouteTermGroup) sdk.RouteTermGroup {
	return sdk.RouteTermGroup{
		Any: mapSlice(v.Any, func(v string) string { return v }),
	}
}

func fromSDKRouteToolCall(v sdk.RouteToolCall) memory.RouteToolCall {
	return memory.RouteToolCall{
		Arguments: mapMap(v.Arguments, func(v string) string { return v }, func(v any) any { return cloneArgument(v) }),
	}
}

func toSDKRouteToolCall(v memory.RouteToolCall) sdk.RouteToolCall {
	return sdk.RouteToolCall{
		Arguments: mapMap(v.Arguments, func(v string) string { return v }, func(v any) any { return cloneArgument(v) }),
	}
}

func fromSDKRouteToolRequirement(v sdk.RouteToolRequirement) memory.RouteToolRequirement {
	return memory.RouteToolRequirement{
		Name:  v.Name,
		Calls: mapSlice(v.Calls, func(v sdk.RouteToolCall) memory.RouteToolCall { return fromSDKRouteToolCall(v) }),
	}
}

func toSDKRouteToolRequirement(v memory.RouteToolRequirement) sdk.RouteToolRequirement {
	return sdk.RouteToolRequirement{
		Name:  v.Name,
		Calls: mapSlice(v.Calls, func(v memory.RouteToolCall) sdk.RouteToolCall { return toSDKRouteToolCall(v) }),
	}
}

func toSDKSaveOptions(v memory.SaveOptions) sdk.SaveOptions {
	return sdk.SaveOptions{
		Tags:          mapSlice(v.Tags, func(v string) string { return v }),
		Links:         mapSlice(v.Links, func(v string) string { return v }),
		Aliases:       mapSlice(v.Aliases, func(v string) string { return v }),
		Status:        v.Status,
		ValidFrom:     v.ValidFrom,
		ValidUntil:    mapPointer(v.ValidUntil, func(v time.Time) time.Time { return v }),
		ExpiresAt:     mapPointer(v.ExpiresAt, func(v time.Time) time.Time { return v }),
		StateKey:      v.StateKey,
		StateValue:    v.StateValue,
		Confidence:    v.Confidence,
		Supersedes:    mapSlice(v.Supersedes, func(v string) string { return v }),
		RoutePolicies: mapSlice(v.RoutePolicies, func(v memory.RoutePolicy) sdk.RoutePolicy { return toSDKRoutePolicy(v) }),
	}
}

func fromSDKSaveResult(v sdk.SaveResult) memory.SaveResult {
	return memory.SaveResult{
		ID:              v.ID,
		Path:            v.Path,
		Created:         v.Created,
		UpdatedExisting: v.UpdatedExisting,
		DuplicateOf:     v.DuplicateOf,
	}
}

func toSDKSearchOptions(v memory.SearchOptions) sdk.SearchOptions {
	return sdk.SearchOptions{
		EntryFilter:     toSDKEntryFilter(v.EntryFilter),
		Limit:           v.Limit,
		Category:        v.Category,
		Tier:            mapPointer(v.Tier, func(v memory.Tier) sdk.Tier { return toSDKTier(v) }),
		IncludeInactive: v.IncludeInactive,
		IncludeExpired:  v.IncludeExpired,
		AsOf:            v.AsOf,
		IncludeGraph:    v.IncludeGraph,
		GraphDepth:      v.GraphDepth,
		Explain:         v.Explain,
		SkipAccessStats: v.SkipAccessStats,
	}
}

func fromSDKSearchResult(v sdk.SearchResult) memory.SearchResult {
	return memory.SearchResult{
		Entry:       fromSDKEntry(v.Entry),
		Score:       v.Score,
		DirectScore: v.DirectScore,
		GraphScore:  v.GraphScore,
		Paths:       mapSlice(v.Paths, func(v sdk.ActivationPath) memory.ActivationPath { return fromSDKActivationPath(v) }),
		Excerpts:    mapSlice(v.Excerpts, func(v sdk.ActivationExcerpt) memory.ActivationExcerpt { return fromSDKActivationExcerpt(v) }),
	}
}

func toSDKSearchResult(v memory.SearchResult) sdk.SearchResult {
	return sdk.SearchResult{
		Entry:       toSDKEntry(v.Entry),
		Score:       v.Score,
		DirectScore: v.DirectScore,
		GraphScore:  v.GraphScore,
		Paths:       mapSlice(v.Paths, func(v memory.ActivationPath) sdk.ActivationPath { return toSDKActivationPath(v) }),
		Excerpts:    mapSlice(v.Excerpts, func(v memory.ActivationExcerpt) sdk.ActivationExcerpt { return toSDKActivationExcerpt(v) }),
	}
}

func fromSDKSearchTrace(v sdk.SearchTrace) memory.SearchTrace {
	return memory.SearchTrace{
		Query:      v.Query,
		Mode:       v.Mode,
		Source:     v.Source,
		Limit:      v.Limit,
		GraphDepth: v.GraphDepth,
		Filters:    fromSDKSearchTraceFilters(v.Filters),
		Seeds:      mapSlice(v.Seeds, func(v sdk.SearchTraceNode) memory.SearchTraceNode { return fromSDKSearchTraceNode(v) }),
		Hops:       mapSlice(v.Hops, func(v sdk.SearchTraceHop) memory.SearchTraceHop { return fromSDKSearchTraceHop(v) }),
		Results:    mapSlice(v.Results, func(v sdk.SearchTraceResult) memory.SearchTraceResult { return fromSDKSearchTraceResult(v) }),
		Temporal:   mapSlice(v.Temporal, func(v string) string { return v }),
		Warnings:   mapSlice(v.Warnings, func(v string) string { return v }),
		DurationMS: v.DurationMS,
	}
}

func fromSDKSearchTraceFilters(v sdk.SearchTraceFilters) memory.SearchTraceFilters {
	return memory.SearchTraceFilters{
		Category:        v.Category,
		Tier:            v.Tier,
		IncludeInactive: v.IncludeInactive,
		IncludeExpired:  v.IncludeExpired,
		AsOf:            v.AsOf,
	}
}

func fromSDKSearchTraceHop(v sdk.SearchTraceHop) memory.SearchTraceHop {
	return memory.SearchTraceHop{
		Depth:       v.Depth,
		FromID:      v.FromID,
		FromRef:     v.FromRef,
		ToID:        v.ToID,
		ToRef:       v.ToRef,
		Via:         v.Via,
		Kind:        v.Kind,
		Weight:      v.Weight,
		Boost:       v.Boost,
		SourceScore: v.SourceScore,
		TargetScore: v.TargetScore,
	}
}

func fromSDKSearchTraceNode(v sdk.SearchTraceNode) memory.SearchTraceNode {
	return memory.SearchTraceNode{
		ID:             v.ID,
		Ref:            v.Ref,
		Category:       v.Category,
		Tier:           v.Tier,
		Score:          v.Score,
		DirectScore:    v.DirectScore,
		GraphScore:     v.GraphScore,
		ContentPreview: v.ContentPreview,
	}
}

func fromSDKSearchTraceResult(v sdk.SearchTraceResult) memory.SearchTraceResult {
	return memory.SearchTraceResult{
		SearchTraceNode: fromSDKSearchTraceNode(v.SearchTraceNode),
		Rank:            v.Rank,
	}
}

func fromSDKSessionSummary(v sdk.SessionSummary) memory.SessionSummary {
	return memory.SessionSummary{
		SessionID:     v.SessionID,
		UserID:        v.UserID,
		CreatedAt:     v.CreatedAt,
		Topics:        mapSlice(v.Topics, func(v string) string { return v }),
		KeyDecisions:  mapSlice(v.KeyDecisions, func(v string) string { return v }),
		OpenQuestions: mapSlice(v.OpenQuestions, func(v string) string { return v }),
		CodeContext:   v.CodeContext,
		RawSummary:    v.RawSummary,
	}
}

func toSDKSessionSummary(v memory.SessionSummary) sdk.SessionSummary {
	return sdk.SessionSummary{
		SessionID:     v.SessionID,
		UserID:        v.UserID,
		CreatedAt:     v.CreatedAt,
		Topics:        mapSlice(v.Topics, func(v string) string { return v }),
		KeyDecisions:  mapSlice(v.KeyDecisions, func(v string) string { return v }),
		OpenQuestions: mapSlice(v.OpenQuestions, func(v string) string { return v }),
		CodeContext:   v.CodeContext,
		RawSummary:    v.RawSummary,
	}
}

func fromSDKTemporalResolution(v sdk.TemporalResolution) memory.TemporalResolution {
	return memory.TemporalResolution{
		Entries:        mapSlice(v.Entries, func(v sdk.Entry) memory.Entry { return fromSDKEntry(v) }),
		Notes:          mapSlice(v.Notes, func(v string) string { return v }),
		SupersededRefs: mapSlice(v.SupersededRefs, func(v string) string { return v }),
		ConflictRefs:   mapSlice(v.ConflictRefs, func(v string) string { return v }),
		ExpiredRefs:    mapSlice(v.ExpiredRefs, func(v string) string { return v }),
		FutureRefs:     mapSlice(v.FutureRefs, func(v string) string { return v }),
	}
}

func toSDKTidalFeedback(v memory.TidalFeedback) sdk.TidalFeedback {
	return sdk.TidalFeedback{
		Query:   v.Query,
		QueryID: v.QueryID,
		Entry:   toSDKEntry(v.Entry),
		Signal:  v.Signal,
		Value:   v.Value,
		At:      v.At,
		Keys:    mapSlice(v.Keys, func(v string) string { return v }),
	}
}

func fromSDKTidalKernelSnapshot(v sdk.TidalKernelSnapshot) diagnostic.TidalKernelSnapshot {
	return diagnostic.TidalKernelSnapshot{
		Key:      v.Key,
		Feature:  v.Feature,
		BinEdges: mapSlice(v.BinEdges, func(v time.Duration) time.Duration { return v }),
		Weights:  mapSlice(v.Weights, func(v float64) float64 { return v }),
		Counts:   mapSlice(v.Counts, func(v int) int { return v }),
	}
}

func toSDKTidalKernelSnapshot(v diagnostic.TidalKernelSnapshot) sdk.TidalKernelSnapshot {
	return sdk.TidalKernelSnapshot{
		Key:      v.Key,
		Feature:  v.Feature,
		BinEdges: mapSlice(v.BinEdges, func(v time.Duration) time.Duration { return v }),
		Weights:  mapSlice(v.Weights, func(v float64) float64 { return v }),
		Counts:   mapSlice(v.Counts, func(v int) int { return v }),
	}
}

func fromSDKTidalRerankerConfig(v sdk.TidalRerankerConfig) diagnostic.TidalRerankerConfig {
	return diagnostic.TidalRerankerConfig{
		Beta:         v.Beta,
		MaxBoost:     v.MaxBoost,
		LearningRate: v.LearningRate,
		MinSamples:   v.MinSamples,
		Bins:         mapSlice(v.Bins, func(v time.Duration) time.Duration { return v }),
	}
}

func toSDKTidalRerankerConfig(v diagnostic.TidalRerankerConfig) sdk.TidalRerankerConfig {
	return sdk.TidalRerankerConfig{
		Beta:         v.Beta,
		MaxBoost:     v.MaxBoost,
		LearningRate: v.LearningRate,
		MinSamples:   v.MinSamples,
		Bins:         mapSlice(v.Bins, func(v time.Duration) time.Duration { return v }),
	}
}

func fromSDKTidalStoreStats(v sdk.TidalStoreStats) diagnostic.TidalStoreStats {
	return diagnostic.TidalStoreStats{
		QueryEvents:    v.QueryEvents,
		RecallEvents:   v.RecallEvents,
		FeedbackEvents: v.FeedbackEvents,
		Kernels:        v.Kernels,
	}
}

func fromSDKTier(v sdk.Tier) memory.Tier {
	return memory.Tier(v)
}

func toSDKTier(v memory.Tier) sdk.Tier {
	return sdk.Tier(v)
}

func fromSDKUnlinkedMention(v sdk.UnlinkedMention) memory.UnlinkedMention {
	return memory.UnlinkedMention{
		SourceID: v.SourceID,
		Source:   v.Source,
		Name:     v.Name,
		TargetID: v.TargetID,
		Line:     v.Line,
		Excerpt:  v.Excerpt,
	}
}
