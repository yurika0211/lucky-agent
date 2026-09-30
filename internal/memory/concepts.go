// Built-in concept notes, query-term aliases, and concept-link canonicalization.
package memory

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

func extractQueryTerms(query string) []string {
	var terms []string
	var latin strings.Builder
	var han []rune

	flushLatin := func() {
		if latin.Len() == 0 {
			return
		}
		token := strings.ToLower(latin.String())
		if len([]rune(token)) >= 2 {
			terms = append(terms, token)
		}
		latin.Reset()
	}
	flushHan := func() {
		if len(han) == 0 {
			return
		}
		if len(han) == 1 {
			han = han[:0]
			return
		}
		if len(han) <= 4 {
			terms = append(terms, string(han))
		}
		for n := 2; n <= 4; n++ {
			if len(han) < n {
				continue
			}
			for i := 0; i+n <= len(han); i++ {
				terms = append(terms, string(han[i:i+n]))
			}
		}
		han = han[:0]
	}

	for _, r := range query {
		switch {
		case unicode.Is(unicode.Han, r):
			flushLatin()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushHan()
			latin.WriteRune(unicode.ToLower(r))
		default:
			flushLatin()
			flushHan()
		}
	}
	flushLatin()
	flushHan()
	return expandQueryTerms(query, dedupSlice(terms))
}

type queryAliasRule struct {
	Triggers []string
	Aliases  []string
}

var queryAliasRules = []queryAliasRule{
	{Triggers: []string{"女儿", "闺女", "daughter"}, Aliases: []string{"daughter", "child", "family"}},
	{Triggers: []string{"儿子", "son"}, Aliases: []string{"son", "child", "family"}},
	{Triggers: []string{"孩子", "小孩", "儿童", "小朋友", "带娃", "child", "kid"}, Aliases: []string{"child", "daughter", "son", "family"}},
	{Triggers: []string{"花粉", "花粉症", "pollen", "hay fever"}, Aliases: []string{"pollen allergy", "pollen", "allergy", "hay fever"}},
	{Triggers: []string{"过敏", "allergy", "allergic"}, Aliases: []string{"pollen allergy", "allergy", "hay fever"}},
	{Triggers: []string{"出门", "外出", "户外", "公园", "踏青", "郊游", "outdoor", "park"}, Aliases: []string{"outdoor plan", "outdoor", "park"}},
	{Triggers: []string{"天气", "下雨", "气温", "温度", "forecast", "weather"}, Aliases: []string{"weather forecast", "weather"}},
	{Triggers: []string{"空气质量", "空气", "雾霾", "aqi", "pm2.5"}, Aliases: []string{"air quality", "aqi"}},
	{Triggers: []string{"上海", "shanghai"}, Aliases: []string{"shanghai"}},
}

type conceptRule struct {
	Concept  string
	Triggers []string
	Aliases  []string
	Tags     []string
}

type conceptSpec struct {
	Name    string
	Aliases []string
	Tags    []string
	Related []string
}

var builtInConceptRules = []conceptRule{
	{
		Concept:  "LuckyAgent",
		Triggers: []string{"luckyagent", "lh", "l h"},
		Aliases:  []string{"lh"},
		Tags:     []string{"concept/luckyagent"},
	},
	{
		Concept:  "LuckyAgent Memory",
		Triggers: []string{"luckyagent memory", "lh memory", "记忆库", "durable memory", "working memory", "memory vault", "obsidian-first", "graph memory", "双链记忆"},
		Aliases:  []string{"记忆库", "graph memory", "working memory"},
		Tags:     []string{"concept/memory"},
	},
	{
		Concept:  "Obsidian",
		Triggers: []string{"obsidian", "双链", "wikilink", "backlink", "vault"},
		Aliases:  []string{"双链", "wikilink", "backlink"},
		Tags:     []string{"concept/obsidian"},
	},
	{
		Concept:  "Message Gateway",
		Triggers: []string{"msg-gateway", "message gateway", "gateway", "网关", "消息网关", "渠道"},
		Aliases:  []string{"网关", "消息网关", "gateway"},
		Tags:     []string{"concept/gateway"},
	},
	{
		Concept:  "QQ Official",
		Triggers: []string{"qq official", "qqofficial", "qq 官方", "qq官方", "官方渠道", "官方频道"},
		Aliases:  []string{"QQ官方", "官方渠道", "官方频道"},
		Tags:     []string{"concept/gateway", "gateway/qqofficial"},
	},
	{
		Concept:  "Reasoning Content",
		Triggers: []string{"reasoning_content", "reasoning content", "chain-of-thought", "chain of thought", "cot", "思维链", "推理内容"},
		Aliases:  []string{"思维链", "推理内容", "chain-of-thought"},
		Tags:     []string{"concept/reasoning"},
	},
	{
		Concept:  "Gateway Trace",
		Triggers: []string{"trace", "progress trace", "tool trace", "reasoning trace", "轨迹", "进度卡片", "工具轨迹"},
		Aliases:  []string{"trace", "进度轨迹", "工具轨迹"},
		Tags:     []string{"concept/trace"},
	},
	{
		Concept:  "Session Memory",
		Triggers: []string{"session", "sessions", "会话", "历史会话", "session history"},
		Aliases:  []string{"会话", "session history"},
		Tags:     []string{"concept/session"},
	},
	{
		Concept:  "RAG",
		Triggers: []string{"rag", "retrieval augmented", "检索增强", "向量召回"},
		Aliases:  []string{"检索增强", "向量召回"},
		Tags:     []string{"concept/rag"},
	},
}

func enrichSaveOptionsWithConcepts(content, category string, opts SaveOptions) SaveOptions {
	links, aliases, tags := inferConceptMetadata(content, category)
	if len(links) > 0 {
		opts.Links = normalizeLinks(append(opts.Links, links...))
	}
	if len(aliases) > 0 {
		opts.Aliases = dedupSlice(append(opts.Aliases, aliases...))
	}
	if len(tags) > 0 {
		opts.Tags = mergeTags(opts.Tags, tags)
	}
	return opts
}

func inferConceptMetadata(content, category string) (links, aliases, tags []string) {
	text := strings.ToLower(strings.Join([]string{category, content}, "\n"))
	for _, rule := range builtInConceptRules {
		if !conceptRuleMatches(text, rule.Triggers) {
			continue
		}
		links = append(links, rule.Concept)
		aliases = append(aliases, rule.Aliases...)
		tags = append(tags, rule.Tags...)
	}
	return normalizeLinks(links), dedupSlice(aliases), dedupSlice(tags)
}

func conceptRuleMatches(text string, triggers []string) bool {
	for _, trigger := range triggers {
		trigger = strings.ToLower(strings.TrimSpace(trigger))
		if trigger != "" && strings.Contains(text, trigger) {
			return true
		}
	}
	return false
}

func init() {
	for _, rule := range builtInConceptRules {
		queryAliasRules = append(queryAliasRules, queryAliasRule{
			Triggers: append([]string{rule.Concept}, rule.Aliases...),
			Aliases:  append([]string{rule.Concept}, rule.Aliases...),
		})
	}
}

func (s *Store) ensureConceptEntriesLocked(links []string) []string {
	queue := normalizeMemoryLinks(links)
	seen := make(map[string]bool, len(queue))
	var created []string
	for len(queue) > 0 {
		link := queue[0]
		queue = queue[1:]
		spec, ok := conceptSpecForLink(link)
		if !ok {
			continue
		}
		key := graphKey(spec.Name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		queue = append(queue, spec.Related...)
		if s.hasConceptEntryLocked(spec.Name) {
			continue
		}
		now := time.Now()
		id := conceptEntryID(spec.Name)
		entry := &Entry{
			ID:         id,
			Content:    spec.Name,
			Category:   "concept",
			Tier:       TierLong,
			Importance: 0.85,
			CreatedAt:  now,
			AccessedAt: now,
			Tags:       mergeTags([]string{"concept"}, spec.Tags),
			Aliases:    dedupSlice(spec.Aliases),
			Status:     "active",
			ValidFrom:  now,
			BlockID:    blockIDForEntry(id),
			Path:       conceptNotePath(spec.Name),
		}
		entry.Links = normalizeMemoryLinks(spec.Related)
		s.entries[entry.ID] = entry
		s.paths[entry.ID] = entry.Path
		s.indexEntryLocked(entry)
		created = append(created, entry.ID)
	}
	return created
}

func conceptSpecForLink(link string) (conceptSpec, bool) {
	link = strings.TrimSpace(link)
	if link == "" || memoryInternalLinkTarget(link) {
		return conceptSpec{}, false
	}
	if rule, ok := conceptRuleForName(link); ok {
		return conceptSpec{
			Name:    rule.Concept,
			Aliases: dedupSlice(rule.Aliases),
			Tags:    dedupSlice(rule.Tags),
			Related: normalizeMemoryLinks(conceptRelatedLinks(rule)),
		}, true
	}
	return conceptSpec{Name: link}, true
}

func memoryInternalLinkTarget(link string) bool {
	link = strings.ToLower(strings.TrimSpace(link))
	if link == "" {
		return true
	}
	return strings.HasPrefix(link, "mem_")
}

func conceptRuleForName(name string) (conceptRule, bool) {
	key := graphKey(name)
	for _, rule := range builtInConceptRules {
		if graphKey(rule.Concept) == key {
			return rule, true
		}
		for _, alias := range rule.Aliases {
			if graphKey(alias) == key {
				return rule, true
			}
		}
	}
	return conceptRule{}, false
}

func (s *Store) hasConceptEntryLocked(concept string) bool {
	id := conceptEntryID(concept)
	if _, ok := s.entries[id]; ok {
		return true
	}
	key := graphKey(concept)
	for _, entry := range s.entries {
		if entry == nil || !strings.EqualFold(strings.TrimSpace(entry.Category), "concept") {
			continue
		}
		if graphKey(entry.Content) == key {
			return true
		}
		for _, alias := range entry.Aliases {
			if graphKey(alias) == key {
				return true
			}
		}
	}
	return false
}

func conceptEntryID(concept string) string {
	slug := slugify(concept)
	if slug == "" {
		h := fnv.New32a()
		_, _ = h.Write([]byte(concept))
		slug = fmt.Sprintf("u%x", h.Sum32())
	}
	return "concept_" + strings.ReplaceAll(slug, "-", "_")
}

func conceptNotePath(concept string) string {
	return filepath.ToSlash(filepath.Join("70_Concepts", humanFileTitle(concept, "Concept")+".md"))
}

func conceptRelatedLinks(rule conceptRule) []string {
	switch rule.Concept {
	case "QQ Official":
		return []string{"Message Gateway", "Gateway Trace", "Reasoning Content"}
	case "Reasoning Content":
		return []string{"Gateway Trace", "QQ Official"}
	case "Gateway Trace":
		return []string{"Message Gateway", "Reasoning Content"}
	case "LuckyAgent Memory":
		return []string{"LuckyAgent", "Obsidian", "RAG"}
	case "Obsidian":
		return []string{"LuckyAgent Memory"}
	case "Session Memory":
		return []string{"LuckyAgent Memory"}
	case "RAG":
		return []string{"LuckyAgent Memory"}
	case "Message Gateway":
		return []string{"LuckyAgent"}
	default:
		return nil
	}
}

func expandQueryTerms(queryLower string, terms []string) []string {
	out := append([]string(nil), terms...)
	for _, rule := range queryAliasRules {
		if queryAliasRuleMatches(queryLower, terms, rule.Triggers) {
			out = append(out, rule.Aliases...)
		}
	}
	return dedupSlice(out)
}

func queryAliasRuleMatches(queryLower string, terms []string, triggers []string) bool {
	for _, trigger := range triggers {
		trigger = strings.ToLower(strings.TrimSpace(trigger))
		if trigger == "" {
			continue
		}
		if strings.Contains(queryLower, trigger) {
			return true
		}
		for _, term := range terms {
			if term == trigger {
				return true
			}
		}
	}
	return false
}
