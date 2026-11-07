package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

const (
	storyToolName          = "story_mode"
	storySessionFile       = "story-mode.json"
	storyCharactersDirName = "characters"

	storyScopeScene    = "scene"
	storyScopeRelation = "relation"
	storyScopeEvent    = "event"
	storyScopeIdentity = "identity"
)

// CharacterCard is a reusable persona stored outside any session.
// An entered session keeps the snapshot taken at enter.
type CharacterCard struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Personality string `json:"personality"`
	Speech      string `json:"speech"`
	World       string `json:"world"`
	Relations   string `json:"relations"`
	Greeting    string `json:"greeting"`
	Forbidden   string `json:"forbidden"`
	Body        string `json:"body,omitempty"`
	Path        string `json:"-"`
}

// StoryFact is one piece of in-story state. It never enters long-term memory
// unless the user explicitly asks to save a fact through the existing memory path.
type StoryFact struct {
	Scope   string     `json:"scope"`
	Key     string     `json:"key"`
	Value   string     `json:"value"`
	Expires *time.Time `json:"expires,omitempty"`
}

// StorySession is the active story binding for one chat session.
type StorySession struct {
	Active      bool          `json:"active"`
	CharacterID string        `json:"character_id"`
	EnteredAt   time.Time     `json:"entered_at"`
	Snapshot    CharacterCard `json:"snapshot"`
	Node        string        `json:"node,omitempty"`
	Facts       []StoryFact   `json:"facts,omitempty"`
}

// storyModeStore keeps cards in the user config dir and story state beside
// the session, separate from durable memory.
type storyModeStore struct {
	home string
	now  func() time.Time
}

func (a *Agent) storyStore() storyModeStore {
	home := ""
	if a != nil && a.cfg != nil {
		if dir := strings.TrimSpace(a.cfg.HomeDir()); dir != "" {
			home = dir
		}
	}
	if home == "" {
		if env := strings.TrimSpace(os.Getenv("LUCKYAGENT_HOME")); env != "" {
			home = env
		}
	}
	return storyModeStore{home: home, now: time.Now}
}

func (s storyModeStore) charactersDir() string {
	if strings.TrimSpace(s.home) == "" {
		return ""
	}
	return filepath.Join(s.home, storyCharactersDirName)
}

func (s storyModeStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (a *Agent) registerStoryModeTool() {
	if a == nil || a.tools == nil {
		return
	}
	a.tools.Register(&tool.Tool{
		Name:         storyToolName,
		Description:  "Enter or leave an optional text story. action=enter copies a character card into this session. action=leave returns to the normal agent. action=state reads scene, relations, events, and identity. action=advance records one story fact. action=restart returns to a named node. Story facts stay out of long-term memory.",
		Category:     tool.CatBuiltin,
		Source:       "builtin",
		Permission:   tool.PermAuto,
		ParallelSafe: false,
		Parameters: map[string]tool.Param{
			"action":       {Type: "string", Description: "enter, leave, state, advance, or restart", Required: true},
			"character_id": {Type: "string", Description: "Card id for action=enter. Cards live in characters/<id>.md", Required: false},
			"scope":        {Type: "string", Description: "For action=advance: scene, relation, event, or identity", Required: false},
			"key":          {Type: "string", Description: "Fact name, such as place, partner, or user", Required: false},
			"value":        {Type: "string", Description: "Fact text, or the node name for action=restart", Required: false},
			"expires":      {Type: "string", Description: "Optional RFC3339 expiry for one story fact", Required: false},
		},
		Handler: a.handleStoryMode,
	})
}

func (a *Agent) handleStoryMode(args map[string]any) (string, error) {
	action, _ := args["action"].(string)
	sessionID, _ := args["session_id"].(string)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "enter":
		id, _ := args["character_id"].(string)
		state, err := a.enterStoryMode(sessionID, id)
		if err != nil {
			return "", err
		}
		return storyModeJSON(map[string]any{
			"ok": true, "action": "enter", "active": true,
			"character_id": state.CharacterID, "name": state.Snapshot.Name,
			"greeting": state.Snapshot.Greeting,
		}), nil
	case "leave":
		if err := a.leaveStoryMode(sessionID); err != nil {
			return "", err
		}
		return storyModeJSON(map[string]any{"ok": true, "action": "leave", "active": false}), nil
	case "state":
		state, err := a.loadStorySession(sessionID)
		if err != nil {
			return "", err
		}
		return storyModeJSON(map[string]any{"ok": true, "action": "state", "story": a.visibleStory(state)}), nil
	case "advance":
		scope, _ := args["scope"].(string)
		key, _ := args["key"].(string)
		value, _ := args["value"].(string)
		var expires *time.Time
		if raw, _ := args["expires"].(string); strings.TrimSpace(raw) != "" {
			parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
			if err != nil {
				return "", fmt.Errorf("expires must be RFC3339: %w", err)
			}
			expires = &parsed
		}
		state, err := a.advanceStory(sessionID, scope, key, value, expires)
		if err != nil {
			return "", err
		}
		return storyModeJSON(map[string]any{"ok": true, "action": "advance", "story": a.visibleStory(state)}), nil
	case "restart":
		node, _ := args["value"].(string)
		if strings.TrimSpace(node) == "" {
			node, _ = args["key"].(string)
		}
		state, err := a.restartStory(sessionID, node)
		if err != nil {
			return "", err
		}
		return storyModeJSON(map[string]any{"ok": true, "action": "restart", "node": state.Node, "greeting": state.Snapshot.Greeting}), nil
	default:
		return "", fmt.Errorf("invalid story_mode action %q (use enter, leave, state, advance, restart)", action)
	}
}

func storyModeJSON(payload map[string]any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func (a *Agent) enterStoryMode(sessionID, characterID string) (StorySession, error) {
	card, err := a.storyStore().LoadCard(characterID)
	if err != nil {
		return StorySession{}, err
	}
	state, err := a.loadStorySession(sessionID)
	if err != nil && !os.IsNotExist(err) {
		return StorySession{}, err
	}
	state.Active = true
	state.CharacterID = card.ID
	state.EnteredAt = a.storyStore().clock()
	state.Snapshot = card
	if strings.TrimSpace(state.Node) == "" {
		state.Node = "start"
	}
	if err := a.saveStorySession(sessionID, state); err != nil {
		return StorySession{}, err
	}
	return state, nil
}

func (a *Agent) leaveStoryMode(sessionID string) error {
	state, err := a.loadStorySession(sessionID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	state.Active = false
	return a.saveStorySession(sessionID, state)
}

func (a *Agent) advanceStory(sessionID, scope, key, value string, expires *time.Time) (StorySession, error) {
	state, err := a.loadStorySession(sessionID)
	if err != nil {
		return StorySession{}, err
	}
	if !state.Active {
		return StorySession{}, fmt.Errorf("story mode is not active")
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch scope {
	case storyScopeScene, storyScopeRelation, storyScopeEvent, storyScopeIdentity:
	default:
		return StorySession{}, fmt.Errorf("invalid story scope %q", scope)
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return StorySession{}, fmt.Errorf("story fact key and value are required")
	}
	next := make([]StoryFact, 0, len(state.Facts)+1)
	for _, fact := range state.Facts {
		if fact.Scope == scope && fact.Key == key {
			continue
		}
		next = append(next, fact)
	}
	next = append(next, StoryFact{Scope: scope, Key: key, Value: value, Expires: expires})
	state.Facts = next
	if err := a.saveStorySession(sessionID, state); err != nil {
		return StorySession{}, err
	}
	return state, nil
}

func (a *Agent) restartStory(sessionID, node string) (StorySession, error) {
	state, err := a.loadStorySession(sessionID)
	if err != nil {
		return StorySession{}, err
	}
	if !state.Active {
		return StorySession{}, fmt.Errorf("story mode is not active")
	}
	node = strings.TrimSpace(node)
	if node == "" {
		node = "start"
	}
	state.Node = node
	state.Facts = nil
	if err := a.saveStorySession(sessionID, state); err != nil {
		return StorySession{}, err
	}
	return state, nil
}

func (a *Agent) storyActive(sessionID string) (StorySession, bool) {
	state, err := a.loadStorySession(sessionID)
	if err != nil || !state.Active || strings.TrimSpace(state.Snapshot.Name) == "" {
		return StorySession{}, false
	}
	return state, true
}

func (s storyModeStore) LoadCard(id string) (CharacterCard, error) {
	id = sanitizeStoryID(id)
	if id == "" {
		return CharacterCard{}, fmt.Errorf("character_id is required")
	}
	dir := s.charactersDir()
	if dir == "" {
		return CharacterCard{}, fmt.Errorf("character card directory is not configured")
	}
	path := filepath.Join(dir, id+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return CharacterCard{}, fmt.Errorf("read character card %s: %w", id, err)
	}
	card, err := parseCharacterCard(id, string(data))
	if err != nil {
		return CharacterCard{}, err
	}
	card.Path = path
	return card, nil
}

func (s storyModeStore) SaveCard(card CharacterCard) error {
	card.ID = sanitizeStoryID(card.ID)
	if card.ID == "" {
		card.ID = sanitizeStoryID(card.Name)
	}
	if card.ID == "" {
		return fmt.Errorf("character card id is required")
	}
	dir := s.charactersDir()
	if dir == "" {
		return fmt.Errorf("character card directory is not configured")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, card.ID+".md"), []byte(formatCharacterCard(card)), 0o600)
}

func parseCharacterCard(id, text string) (CharacterCard, error) {
	card := CharacterCard{ID: sanitizeStoryID(id)}
	body := text
	if strings.HasPrefix(strings.TrimLeft(text, "\ufeff"), "---") {
		scanner := bufio.NewScanner(strings.NewReader(text))
		scanner.Scan()
		fields := map[string]string{}
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "---" {
				break
			}
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			fields[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
		var rest strings.Builder
		for scanner.Scan() {
			rest.WriteString(scanner.Text())
			rest.WriteByte('\n')
		}
		body = rest.String()
		card.Name = fields["name"]
		card.Personality = fields["personality"]
		card.Speech = fields["speech"]
		card.World = fields["world"]
		card.Relations = fields["relations"]
		card.Greeting = fields["greeting"]
		card.Forbidden = fields["forbidden"]
	}
	card.Body = strings.TrimSpace(body)
	if strings.TrimSpace(card.Name) == "" {
		return CharacterCard{}, fmt.Errorf("character card %s has no name", id)
	}
	if card.ID == "" {
		card.ID = sanitizeStoryID(card.Name)
	}
	return card, nil
}

func formatCharacterCard(card CharacterCard) string {
	var b strings.Builder
	b.WriteString("---\n")
	writeCardField(&b, "name", card.Name)
	writeCardField(&b, "personality", card.Personality)
	writeCardField(&b, "speech", card.Speech)
	writeCardField(&b, "world", card.World)
	writeCardField(&b, "relations", card.Relations)
	writeCardField(&b, "greeting", card.Greeting)
	writeCardField(&b, "forbidden", card.Forbidden)
	b.WriteString("---\n")
	if strings.TrimSpace(card.Body) != "" {
		b.WriteString(strings.TrimSpace(card.Body))
		b.WriteByte('\n')
	}
	return b.String()
}

func writeCardField(b *strings.Builder, key, value string) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\n", " ")
	if value == "" {
		return
	}
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

func (a *Agent) storySessionPath(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}
	if a != nil && a.sessions != nil {
		if dir := strings.TrimSpace(a.sessions.Dir()); dir != "" {
			return filepath.Join(dir, sessionID, storySessionFile), nil
		}
	}
	home := a.storyStore().home
	if home == "" {
		return "", fmt.Errorf("story session directory is not configured")
	}
	return filepath.Join(home, "sessions", sessionID, storySessionFile), nil
}

func (a *Agent) loadStorySession(sessionID string) (StorySession, error) {
	path, err := a.storySessionPath(sessionID)
	if err != nil {
		return StorySession{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return StorySession{}, err
	}
	var state StorySession
	if err := json.Unmarshal(data, &state); err != nil {
		return StorySession{}, fmt.Errorf("parse story session: %w", err)
	}
	state.Facts = liveStoryFacts(state.Facts, a.storyStore().clock())
	return state, nil
}

func (a *Agent) saveStorySession(sessionID string, state StorySession) error {
	path, err := a.storySessionPath(sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	state.Facts = liveStoryFacts(state.Facts, a.storyStore().clock())
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func liveStoryFacts(facts []StoryFact, now time.Time) []StoryFact {
	if len(facts) == 0 {
		return nil
	}
	out := make([]StoryFact, 0, len(facts))
	for _, fact := range facts {
		if fact.Expires != nil && !fact.Expires.After(now) {
			continue
		}
		out = append(out, fact)
	}
	return out
}

func (a *Agent) visibleStory(state StorySession) StorySession {
	state.Facts = liveStoryFacts(state.Facts, a.storyStore().clock())
	return state
}

// storyPromptBlock is the only character text injected into a turn.
// It uses the snapshot from enter, not the card file as it is now.
func storyPromptBlock(state StorySession) string {
	card := state.Snapshot
	var b strings.Builder
	b.WriteString("【剧情模式】你正在和用户玩一段纯文字角色剧情。用角色的声音回复：开场、舞台指示、动作和推进剧情。不要使用助手口吻，不要提任务清单、工具或系统。不要把剧情写成长期记忆。\n")
	b.WriteString("角色：")
	b.WriteString(card.Name)
	b.WriteByte('\n')
	writePromptLine(&b, "性格", card.Personality)
	writePromptLine(&b, "语气", card.Speech)
	writePromptLine(&b, "世界", card.World)
	writePromptLine(&b, "关系", card.Relations)
	writePromptLine(&b, "开场", card.Greeting)
	writePromptLine(&b, "禁止", card.Forbidden)
	if strings.TrimSpace(card.Body) != "" {
		b.WriteString(strings.TrimSpace(card.Body))
		b.WriteByte('\n')
	}
	b.WriteString("当前节点：")
	b.WriteString(firstStoryText(state.Node, "start"))
	b.WriteByte('\n')
	if len(state.Facts) > 0 {
		b.WriteString("剧情状态：\n")
		for _, fact := range state.Facts {
			fmt.Fprintf(&b, "- %s/%s: %s\n", fact.Scope, fact.Key, fact.Value)
		}
	}
	return strings.TrimSpace(b.String())
}

func writePromptLine(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString(label)
	b.WriteString("：")
	b.WriteString(value)
	b.WriteByte('\n')
}

func firstStoryText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sanitizeStoryID(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(id)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// storyWorkRequest reports that the user wants ordinary agent work.
// Story mode must leave before that work runs, and it does not re-enter.
func storyWorkRequest(text string) bool {
	lower := strings.ToLower(text)
	needles := []string{
		"读文件", "看文件", "打开文件", "读取文件", "文件内容",
		"运行命令", "执行命令", "跑命令", "终端",
		"搜索", "查一下", "上网", "生成图片", "画一张", "画图",
		"file_read", "file_write", "terminal", "web_search", "image_generate",
		"read the file", "read file", "run the command", "run command", "search the web", "generate an image",
	}
	for _, needle := range needles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	// "读一下 README 文件" does not contain the compact phrase 读文件.
	if strings.Contains(lower, "文件") && (strings.Contains(lower, "读") || strings.Contains(lower, "看") || strings.Contains(lower, "打开")) {
		return true
	}
	if strings.Contains(lower, "命令") && (strings.Contains(lower, "跑") || strings.Contains(lower, "运行") || strings.Contains(lower, "执行")) {
		return true
	}
	return false
}

// explicitMemorySaveRequest is the only story-mode path into durable memory.
func explicitMemorySaveRequest(text string) bool {
	lower := strings.ToLower(text)
	needles := []string{
		"记到长期记忆", "写入长期记忆", "保存到记忆", "记住这个事实",
		"save this fact", "save to memory", "remember this fact",
	}
	for _, needle := range needles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// applyStoryModePolicy shapes one turn. Outside story mode it changes nothing.
// Inside, tools stay hidden unless the user asked for real work, which leaves
// story mode before the normal agent runs.
func (a *Agent) applyStoryModePolicy(sess *session.Session, cfg *LoopConfig, routingText string) {
	if a == nil || cfg == nil || sess == nil {
		return
	}
	state, active := a.storyActive(sess.ID)
	if !active {
		return
	}
	if storyWorkRequest(routingText) {
		_ = a.leaveStoryMode(sess.ID)
		return
	}
	cfg.RestrictTools = true
	cfg.AllowedTools = []string{storyToolName}
	if explicitMemorySaveRequest(routingText) {
		cfg.AllowedTools = append(cfg.AllowedTools, "memory", "remember")
	}
	_ = state
}

// storyContextMessage returns the snapshot prompt for an active story session.
func (a *Agent) storyContextMessage(sess *session.Session) string {
	if a == nil || sess == nil {
		return ""
	}
	state, active := a.storyActive(sess.ID)
	if !active {
		return ""
	}
	return storyPromptBlock(state)
}
