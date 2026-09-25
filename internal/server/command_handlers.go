package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/memory"
	"github.com/yurika0211/luckyagent/internal/provider"
)

// commandSpec describes one slash command a UI can run.
type commandSpec struct {
	Name        string `json:"name"`
	Usage       string `json:"usage"`
	Description string `json:"description"`
	Group       string `json:"group"`
}

// webCommandSpecs is the catalog served to UIs. It deliberately mirrors the
// Telegram command names so the same muscle memory works in both places; only
// commands whose behaviour is meaningful outside a chat platform are listed.
func webCommandSpecs() []commandSpec {
	return []commandSpec{
		{Name: "help", Usage: "/help", Description: "List available commands", Group: "basic"},
		{Name: "version", Usage: "/version", Description: "Show runtime version", Group: "system"},
		{Name: "status", Usage: "/status", Description: "Show runtime status", Group: "system"},
		{Name: "health", Usage: "/health", Description: "System health check", Group: "system"},
		{Name: "metrics", Usage: "/metrics", Description: "Show usage metrics", Group: "system"},
		{Name: "tools", Usage: "/tools [all]", Description: "List available tools", Group: "system"},
		{Name: "tool", Usage: "/tool <name>", Description: "Show a tool's details", Group: "system"},
		{Name: "skills", Usage: "/skills [all]", Description: "List loaded skills", Group: "system"},
		{Name: "skill", Usage: "/skill <name>", Description: "Show a skill's details", Group: "system"},
		{Name: "models", Usage: "/models [kind]", Description: "List configured models", Group: "system"},
		{Name: "model", Usage: "/model [name]", Description: "Show or switch the chat model", Group: "system"},
		{Name: "soul", Usage: "/soul", Description: "Show SOUL info", Group: "system"},
		{Name: "config", Usage: "/config [list]", Description: "Show configuration keys", Group: "system"},
		{Name: "rag", Usage: "/rag [stats|search <q>]", Description: "Inspect the knowledge base", Group: "system"},
		{Name: "remember", Usage: "/remember <content>", Description: "Save medium-term memory", Group: "session"},
		{Name: "remember_long", Usage: "/remember_long <content>", Description: "Save long-term memory", Group: "session"},
		{Name: "recall", Usage: "/recall <query>", Description: "Search memory", Group: "session"},
		{Name: "memstats", Usage: "/memstats", Description: "Show memory stats", Group: "session"},
		{Name: "memdecay", Usage: "/memdecay [threshold]", Description: "Decay low-weight memories", Group: "session"},
		{Name: "promote", Usage: "/promote <memory_id>", Description: "Promote a memory to long term", Group: "session"},
		{Name: "sessions", Usage: "/sessions", Description: "List sessions", Group: "session"},
		{Name: "session", Usage: "/session", Description: "Show the current session", Group: "session"},
		{Name: "rename", Usage: "/rename <title>", Description: "Rename the current session", Group: "session"},
	}
}

type commandRequest struct {
	Command   string `json:"command"`
	Args      string `json:"args"`
	SessionID string `json:"session_id"`
}

// handleCommands lists the catalog on GET and runs one command on POST.
func (s *Server) handleCommands(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.sendJSON(w, http.StatusOK, map[string]interface{}{
			"commands": webCommandSpecs(),
			"count":    len(webCommandSpecs()),
		})
	case http.MethodPost:
		var req commandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
			return
		}
		name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(req.Command), "/"))
		name = strings.ReplaceAll(name, "-", "_")
		if name == "" {
			s.sendError(w, "command is required", http.StatusBadRequest, "")
			return
		}
		output, err := s.runCommand(name, strings.TrimSpace(req.Args), strings.TrimSpace(req.SessionID))
		if err != nil {
			s.sendJSON(w, http.StatusOK, map[string]interface{}{
				"command": name,
				"ok":      false,
				"output":  err.Error(),
			})
			return
		}
		s.sendJSON(w, http.StatusOK, map[string]interface{}{
			"command": name,
			"ok":      true,
			"output":  output,
		})
	default:
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
	}
}

func (s *Server) runCommand(name, args, sessionID string) (string, error) {
	a := s.agent
	if a == nil {
		return "", fmt.Errorf("agent runtime unavailable")
	}

	switch name {
	case "help":
		return renderHelp(), nil

	case "version":
		return fmt.Sprintf("**Runtime**\n\n- Go: %s\n- OS/Arch: %s/%s\n- CPUs: %d",
			runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU()), nil

	case "status":
		stats := a.MemoryStats()
		total := stats[memory.TierShort] + stats[memory.TierMedium] + stats[memory.TierLong]
		var toolCount int
		if reg := a.Tools(); reg != nil {
			toolCount = len(reg.ListEnabled())
		}
		var sessionCount int
		if mgr := a.Sessions(); mgr != nil {
			sessionCount = len(mgr.ListInfo())
		}
		model := "n/a"
		if ref, ok := a.CurrentModel(config.ModelKindChat); ok {
			model = ref.ID
		}
		return fmt.Sprintf("**Status**\n\n- Chat model: `%s`\n- Sessions: %d\n- Tools enabled: %d\n- Memories: %d",
			model, sessionCount, toolCount, total), nil

	case "health":
		lines := []string{"**Health**", ""}
		lines = append(lines, checkLine("agent", true))
		lines = append(lines, checkLine("memory store", a.Memory() != nil))
		lines = append(lines, checkLine("tool registry", a.Tools() != nil))
		lines = append(lines, checkLine("session manager", a.Sessions() != nil))
		lines = append(lines, checkLine("RAG", a.RAG() != nil))
		lines = append(lines, checkLine("metrics", a.Metrics() != nil))
		return strings.Join(lines, "\n"), nil

	case "metrics":
		m := a.Metrics()
		if m == nil {
			return "", fmt.Errorf("metrics unavailable")
		}
		snap := m.Snapshot()
		return fmt.Sprintf(
			"**Metrics**\n\n- Total requests: %d\n- Chat requests: %d\n- Errors: %d\n- Tool calls: %d\n- Sessions: %d active / %d total\n- Memory: %d stores, %d recalls\n- Uptime since: %s",
			snap.TotalRequests, snap.ChatRequests, snap.ErrorRequests, snap.ToolCalls,
			snap.ActiveSessions, snap.TotalSessions, snap.MemoryStores, snap.MemoryRecalls,
			snap.StartTime.Format(time.RFC3339)), nil

	case "tools":
		reg := a.Tools()
		if reg == nil {
			return "", fmt.Errorf("tool registry unavailable")
		}
		tools := reg.ListEnabled()
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		limit := 30
		if strings.EqualFold(args, "all") {
			limit = len(tools)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "**Tools** (%d enabled)\n\n", len(tools))
		for i, t := range tools {
			if i >= limit {
				fmt.Fprintf(&b, "\n_…%d more. Use `/tools all`._", len(tools)-limit)
				break
			}
			fmt.Fprintf(&b, "- `%s` — %s\n", t.Name, firstLine(t.Description))
		}
		return b.String(), nil

	case "tool":
		if args == "" {
			return "", fmt.Errorf("usage: /tool <name>")
		}
		reg := a.Tools()
		if reg == nil {
			return "", fmt.Errorf("tool registry unavailable")
		}
		t, ok := reg.Get(args)
		if !ok {
			return "", fmt.Errorf("unknown tool: %s", args)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "**`%s`**\n\n%s\n", t.Name, t.Description)
		if len(t.Parameters) > 0 {
			b.WriteString("\n**Parameters**\n\n")
			names := make([]string, 0, len(t.Parameters))
			for key := range t.Parameters {
				names = append(names, key)
			}
			sort.Strings(names)
			for _, key := range names {
				p := t.Parameters[key]
				fmt.Fprintf(&b, "- `%s` (%s) — %s\n", key, p.Type, p.Description)
			}
		}
		return b.String(), nil

	case "skills":
		skills := a.Skills()
		if len(skills) == 0 {
			return "No skills loaded.", nil
		}
		limit := 30
		if strings.EqualFold(args, "all") {
			limit = len(skills)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "**Skills** (%d loaded)\n\n", len(skills))
		for i, sk := range skills {
			if i >= limit {
				fmt.Fprintf(&b, "\n_…%d more. Use `/skills all`._", len(skills)-limit)
				break
			}
			state := ""
			if !sk.Available {
				state = " _(unavailable)_"
			}
			fmt.Fprintf(&b, "- `%s`%s — %s\n", sk.Name, state, firstLine(sk.Description))
		}
		return b.String(), nil

	case "skill":
		if args == "" {
			return "", fmt.Errorf("usage: /skill <name>")
		}
		for _, sk := range a.Skills() {
			if strings.EqualFold(sk.Name, args) {
				return fmt.Sprintf("**`%s`**\n\n%s\n\n- Directory: `%s`\n- Tools: %d\n- Available: %v",
					sk.Name, sk.Description, sk.Dir, len(sk.Tools), sk.Available), nil
			}
		}
		return "", fmt.Errorf("unknown skill: %s", args)

	case "models":
		// `refresh` is accepted by every client surface. Runtime model
		// configuration is re-applied before listing so custom model/catalog
		// changes become visible immediately; provider discovery remains
		// available through the dedicated models endpoint.
		filter, rest := splitArg(args)
		if strings.EqualFold(filter, "refresh") {
			if a.Config() == nil {
				return "", fmt.Errorf("configuration unavailable")
			}
			cfg := a.Config().Get()
			discovered, err := provider.NewModelDiscovery().Discover(context.Background(), provider.ModelDiscoveryConfig{
				Provider: cfg.Provider, APIKey: cfg.APIKey, APIBase: cfg.APIBase,
				Model: cfg.Model, ExtraHeaders: cfg.ExtraHeaders,
			}, true)
			if err != nil {
				return "", fmt.Errorf("refresh models failed: %w", err)
			}
			if catalog := a.Catalog(); catalog != nil {
				for _, model := range discovered {
					catalog.Register(model)
				}
			}
			filter = ""
			args = ""
		} else {
			args = strings.TrimSpace(args)
		}
		var kind *config.ModelKind
		providerName := ""
		if filter != "" {
			if strings.EqualFold(filter, "provider") {
				providerName = strings.TrimSpace(rest)
				if providerName == "" {
					return "", fmt.Errorf("usage: /models [all|chat|vision|embedding|provider <name>|refresh]")
				}
			} else if !strings.EqualFold(filter, "all") {
				parsed, err := config.ParseModelKind(filter)
				if err != nil || strings.TrimSpace(rest) != "" {
					return "", fmt.Errorf("usage: /models [all|chat|vision|embedding|provider <name>|refresh]")
				}
				kind = &parsed
			}
		}
		models := a.ListModels(kind)
		if providerName != "" {
			filtered := models[:0]
			for _, model := range models {
				if strings.EqualFold(model.Provider, providerName) {
					filtered = append(filtered, model)
				}
			}
			models = filtered
		}
		if len(models) == 0 {
			return "No models configured for this filter.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "**Models** (%d)\n\n", len(models))
		for _, ref := range models {
			marker := ""
			if ref.Current {
				marker = " ← current"
			}
			fmt.Fprintf(&b, "- `%s` · %s · %s%s\n", ref.ID, ref.Kind, ref.Provider, marker)
		}
		return b.String(), nil

	case "model":
		if args == "" {
			var b strings.Builder
			b.WriteString("**Current models**\n\n")
			for _, kind := range []config.ModelKind{
				config.ModelKindChat, config.ModelKindVision, config.ModelKindEmbedding,
			} {
				if ref, ok := a.CurrentModel(kind); ok {
					fmt.Fprintf(&b, "- %s: `%s`\n", kind, ref.ID)
				} else {
					fmt.Fprintf(&b, "- %s: _not set_\n", kind)
				}
			}
			return b.String(), nil
		}
		if err := a.SwitchModel(args); err != nil {
			return "", fmt.Errorf("switch model failed: %w", err)
		}
		return fmt.Sprintf("Chat model switched to `%s`.", args), nil

	case "soul":
		sl := a.Soul()
		if sl == nil {
			return "No SOUL loaded.", nil
		}
		return fmt.Sprintf("**SOUL**\n\n- File: `%s`\n- Size: %d characters\n\n%s",
			sl.FilePath, len([]rune(sl.Content)), firstLine(sl.Content)), nil

	case "config":
		mgr := a.Config()
		if mgr == nil {
			return "", fmt.Errorf("configuration unavailable")
		}
		cfg := mgr.Get()
		raw, err := json.Marshal(cfg)
		if err != nil {
			return "", fmt.Errorf("read config: %w", err)
		}
		var generic map[string]interface{}
		if err := json.Unmarshal(raw, &generic); err != nil {
			return "", fmt.Errorf("read config: %w", err)
		}
		keys := make([]string, 0, len(generic))
		for key := range generic {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		// Values are withheld on purpose: the config carries API keys, and this
		// output is rendered straight into a chat transcript.
		return "**Configuration sections**\n\n- `" + strings.Join(keys, "`\n- `") +
			"`\n\n_Values are hidden here; edit them in Settings._", nil

	case "rag":
		mgr := a.RAG()
		if mgr == nil {
			return "", fmt.Errorf("RAG is not initialized")
		}
		action, rest := splitArg(args)
		switch strings.ToLower(action) {
		case "", "stats":
			st := mgr.Stats()
			return fmt.Sprintf("**RAG**\n\n- Documents: %d\n- Chunks: %d\n- Estimated tokens: %d",
				st.DocumentCount, st.ChunkCount, st.TotalTokens), nil
		case "search":
			if rest == "" {
				return "", fmt.Errorf("usage: /rag search <query>")
			}
			docs := mgr.ListDocuments()
			return fmt.Sprintf("Indexed documents: %d. Full-text search from the web UI is not wired yet — use the RAG API.", len(docs)), nil
		default:
			return "", fmt.Errorf("usage: /rag [stats|search <query>]")
		}

	case "remember", "remember_long":
		if args == "" {
			return "", fmt.Errorf("usage: /%s <content>", name)
		}
		var err error
		if name == "remember" {
			err = a.Remember(args, "note")
		} else {
			err = a.RememberLongTerm(args, "note")
		}
		if err != nil {
			return "", fmt.Errorf("save memory failed: %w", err)
		}
		return "Saved.", nil

	case "recall":
		if args == "" {
			return "", fmt.Errorf("usage: /recall <query>")
		}
		entries := a.Recall(args)
		if len(entries) == 0 {
			return fmt.Sprintf("No memories matched %q.", args), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "**Recall** — %d match(es)\n\n", len(entries))
		for i, entry := range entries {
			if i >= 10 {
				fmt.Fprintf(&b, "\n_…%d more._", len(entries)-10)
				break
			}
			fmt.Fprintf(&b, "- `%s` [%s] %s\n", entry.ID, entry.Tier, firstLine(entry.Content))
		}
		return b.String(), nil

	case "memstats":
		stats := a.MemoryStats()
		total := stats[memory.TierShort] + stats[memory.TierMedium] + stats[memory.TierLong]
		return fmt.Sprintf("**Memory**\n\n- Short: %d\n- Medium: %d\n- Long: %d\n- Total: %d",
			stats[memory.TierShort], stats[memory.TierMedium], stats[memory.TierLong], total), nil

	case "memdecay":
		threshold := 0.1
		if args != "" {
			parsed, err := strconv.ParseFloat(args, 64)
			if err != nil {
				return "", fmt.Errorf("usage: /memdecay [threshold]")
			}
			threshold = parsed
		}
		removed := a.DecayMemory(threshold)
		return fmt.Sprintf("Decayed %d memories below weight %.2f.", removed, threshold), nil

	case "promote":
		if args == "" {
			return "", fmt.Errorf("usage: /promote <memory_id>")
		}
		if err := a.PromoteMemory(args); err != nil {
			return "", fmt.Errorf("promote failed: %w", err)
		}
		return fmt.Sprintf("Promoted `%s` to long term.", args), nil

	case "sessions":
		mgr := a.Sessions()
		if mgr == nil {
			return "", fmt.Errorf("session manager unavailable")
		}
		infos := mgr.ListInfo()
		var b strings.Builder
		fmt.Fprintf(&b, "**Sessions** (%d)\n\n", len(infos))
		for i, info := range infos {
			if i >= 15 {
				fmt.Fprintf(&b, "\n_…%d more._", len(infos)-15)
				break
			}
			fmt.Fprintf(&b, "- `%s` — %s · %d messages\n", info.ID, info.Title, info.MessageCount)
		}
		return b.String(), nil

	case "session":
		mgr := a.Sessions()
		if mgr == nil || sessionID == "" {
			return "", fmt.Errorf("no active session")
		}
		sess, ok := mgr.Get(sessionID)
		if !ok {
			return "", fmt.Errorf("session not found: %s", sessionID)
		}
		return fmt.Sprintf("**Session**\n\n- ID: `%s`\n- Title: %s\n- Messages: %d\n- Created: %s",
			sess.ID, sess.Title, sess.MessageCount(), sess.CreatedAt.Format(time.RFC3339)), nil

	case "rename":
		if args == "" {
			return "", fmt.Errorf("usage: /rename <title>")
		}
		mgr := a.Sessions()
		if mgr == nil || sessionID == "" {
			return "", fmt.Errorf("no active session")
		}
		sess, ok := mgr.Get(sessionID)
		if !ok {
			return "", fmt.Errorf("session not found: %s", sessionID)
		}
		sess.SetTitle(args)
		return fmt.Sprintf("Session renamed to %q.", args), nil
	}

	return "", fmt.Errorf("`/%s` is not available in the web UI. Run `/help` to see what is", name)
}

func renderHelp() string {
	specs := webCommandSpecs()
	groups := []string{"basic", "system", "session"}
	titles := map[string]string{"basic": "Basic", "system": "System", "session": "Session and memory"}

	var b strings.Builder
	fmt.Fprintf(&b, "**Commands** (%d)\n", len(specs))
	for _, group := range groups {
		wrote := false
		for _, spec := range specs {
			if spec.Group != group {
				continue
			}
			if !wrote {
				fmt.Fprintf(&b, "\n**%s**\n\n", titles[group])
				wrote = true
			}
			fmt.Fprintf(&b, "- `%s` — %s\n", spec.Usage, spec.Description)
		}
	}
	b.WriteString("\n_Anything else is sent to the agent as a normal message._")
	return b.String()
}

func checkLine(label string, ok bool) string {
	if ok {
		return fmt.Sprintf("- %s: ok", label)
	}
	return fmt.Sprintf("- %s: **unavailable**", label)
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	if len([]rune(text)) > 100 {
		return string([]rune(text)[:100]) + "…"
	}
	return text
}

func splitArg(args string) (string, string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", ""
	}
	parts := strings.SplitN(args, " ", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.TrimSpace(parts[1])
}
