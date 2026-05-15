package server

import (
	"net/http"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/contextx"
)

// ===== v0.13.0: Context Window API =====

// handleContext 上下文窗口状态查询
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	cw := s.agent.ContextWindow()
	cfg := cw.Config()

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"max_tokens":             cfg.MaxTokens,
		"reserved_tokens":        cfg.ReservedTokens,
		"available_tokens":       cfg.MaxTokens - cfg.ReservedTokens,
		"strategy":               cfg.Strategy.String(),
		"sliding_window_size":    cfg.SlidingWindowSize,
		"max_conversation_turns": cfg.MaxConversationTurns,
		"memory_budget":          cfg.MemoryBudget,
		"summarize_threshold":    cfg.SummarizeThreshold,
	})
}

// handleContextFit 上下文裁剪接口
func (s *Server) handleContextFit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	var req struct {
		Messages []struct {
			Role     string `json:"role"`
			Content  string `json:"content"`
			Priority int    `json:"priority,omitempty"`
			Category string `json:"category,omitempty"`
		} `json:"messages"`
		Strategy string `json:"strategy,omitempty"` // oldest_first, low_priority_first, sliding_window, summarize
	}

	if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
		return
	}

	// 转换消息
	messages := make([]contextx.Message, len(req.Messages))
	for i, msg := range req.Messages {
		priority := contextx.PriorityNormal
		if msg.Priority > 0 {
			priority = contextx.MessagePriority(msg.Priority)
		}
		if priority < 0 || priority > 3 {
			priority = contextx.PriorityNormal
		}
		category := msg.Category
		if category == "" {
			category = msg.Role
		}

		messages[i] = contextx.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			Priority:  priority,
			Category:  category,
			Timestamp: time.Now(),
		}
	}

	// 选择策略
	cw := s.agent.ContextWindow()
	if req.Strategy != "" {
		switch req.Strategy {
		case "oldest_first":
			cw = contextx.NewContextWindow(contextx.WindowConfig{
				MaxTokens:      cw.Config().MaxTokens,
				ReservedTokens: cw.Config().ReservedTokens,
				Strategy:       contextx.TrimOldest,
			})
		case "low_priority_first":
			cw = contextx.NewContextWindow(contextx.WindowConfig{
				MaxTokens:      cw.Config().MaxTokens,
				ReservedTokens: cw.Config().ReservedTokens,
				Strategy:       contextx.TrimLowPriority,
			})
		case "sliding_window":
			cw = contextx.NewContextWindow(contextx.WindowConfig{
				MaxTokens:         cw.Config().MaxTokens,
				ReservedTokens:    cw.Config().ReservedTokens,
				Strategy:          contextx.TrimSlidingWindow,
				SlidingWindowSize: cw.Config().SlidingWindowSize,
			})
		case "summarize":
			cw = contextx.NewContextWindow(contextx.WindowConfig{
				MaxTokens:      cw.Config().MaxTokens,
				ReservedTokens: cw.Config().ReservedTokens,
				Strategy:       contextx.TrimSummarize,
			})
		}
	}

	// 执行裁剪
	fitted, trimResult := cw.Fit(messages)

	// 转换结果
	resultMessages := make([]map[string]interface{}, len(fitted))
	for i, msg := range fitted {
		resultMessages[i] = map[string]interface{}{
			"role":     msg.Role,
			"content":  msg.Content,
			"priority": int(msg.Priority),
			"category": msg.Category,
		}
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"trimmed":          trimResult.Trimmed,
		"original_count":   trimResult.OriginalCount,
		"original_tokens":  trimResult.OriginalTokens,
		"final_count":      trimResult.FinalCount,
		"final_tokens":     trimResult.FinalTokens,
		"available_tokens": trimResult.AvailableTokens,
		"strategy":         trimResult.Strategy.String(),
		"messages":         resultMessages,
		"summary":          trimResult.Summary(),
	})
}

// --- RAG 知识库 API ---

// handleRAGIndex 索引文档到 RAG 知识库
func (s *Server) handleRAGIndex(w http.ResponseWriter, r *http.Request) {
	s.dispatchMethod(w, r, map[string]func(){
		http.MethodPost: func() {
			var req struct {
				Source  string `json:"source"`            // 文件路径或来源标识
				Title   string `json:"title,omitempty"`   // 文档标题（索引文本时使用）
				Content string `json:"content,omitempty"` // 文本内容（索引文本时使用）
				Dir     string `json:"dir,omitempty"`     // 目录路径（批量索引时使用）
			}

			if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
				s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
				return
			}

			ragMgr := s.agent.RAG()
			if ragMgr == nil {
				s.sendError(w, "RAG not initialized", http.StatusServiceUnavailable, "")
				return
			}

			var result map[string]interface{}
			if req.Dir != "" {
				// 批量索引目录
				docs, err := ragMgr.IndexDirectory(req.Dir)
				if err != nil {
					s.sendError(w, "index directory failed", http.StatusInternalServerError, err.Error())
					return
				}
				docIDs := make([]string, len(docs))
				for i, d := range docs {
					docIDs[i] = d.ID
				}
				result = map[string]interface{}{
					"action":  "index_directory",
					"dir":     req.Dir,
					"indexed": len(docs),
					"doc_ids": docIDs,
				}
			} else if req.Content != "" {
				// 索引文本内容
				title := req.Title
				if title == "" {
					title = req.Source
				}
				doc, err := ragMgr.IndexText(req.Source, title, req.Content)
				if err != nil {
					s.sendError(w, "index text failed", http.StatusInternalServerError, err.Error())
					return
				}
				result = map[string]interface{}{
					"action":     "index_text",
					"doc_id":     doc.ID,
					"title":      doc.Title,
					"chunks":     len(doc.Chunks),
					"indexed_at": doc.IndexedAt,
				}
			} else if req.Source != "" {
				// 索引单个文件
				doc, err := ragMgr.IndexFile(req.Source)
				if err != nil {
					s.sendError(w, "index file failed", http.StatusInternalServerError, err.Error())
					return
				}
				result = map[string]interface{}{
					"action":     "index_file",
					"doc_id":     doc.ID,
					"title":      doc.Title,
					"chunks":     len(doc.Chunks),
					"indexed_at": doc.IndexedAt,
				}
			} else {
				s.sendError(w, "must provide source, content, or dir", http.StatusBadRequest, "")
				return
			}

			s.sendJSON(w, http.StatusOK, result)
		},
		http.MethodDelete: func() {
			var req struct {
				DocID string `json:"doc_id"`
			}
			if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
				s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
				return
			}
			ragMgr := s.agent.RAG()
			if ragMgr == nil {
				s.sendError(w, "RAG not initialized", http.StatusServiceUnavailable, "")
				return
			}
			removed := ragMgr.RemoveDocument(req.DocID)
			s.sendJSON(w, http.StatusOK, map[string]interface{}{
				"doc_id":  req.DocID,
				"removed": removed,
			})
		},
	})
}

// handleRAGSearch 搜索 RAG 知识库
func (s *Server) handleRAGSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	var req struct {
		Query    string  `json:"query"`
		TopK     int     `json:"top_k,omitempty"`
		MinScore float64 `json:"min_score,omitempty"`
		Source   string  `json:"source,omitempty"` // 按来源过滤
	}

	if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
		return
	}

	if req.Query == "" {
		s.sendError(w, "query is required", http.StatusBadRequest, "")
		return
	}

	ragMgr := s.agent.RAG()
	if ragMgr == nil {
		s.sendError(w, "RAG not initialized", http.StatusServiceUnavailable, "")
		return
	}

	// 应用临时检索配置
	if req.TopK > 0 || req.MinScore > 0 || req.Source != "" {
		cfg := ragMgr.RetrieverConfig()
		if req.TopK > 0 {
			cfg.TopK = req.TopK
		}
		if req.MinScore > 0 {
			cfg.MinScore = req.MinScore
		}
		if req.Source != "" {
			cfg.FilterSource = req.Source
		}
		ragMgr.UpdateRetrieverConfig(cfg)
	}

	results, err := ragMgr.Search(r.Context(), req.Query)
	if err != nil {
		s.sendError(w, "search failed", http.StatusInternalServerError, err.Error())
		return
	}

	// 重置过滤
	if req.Source != "" {
		cfg := ragMgr.RetrieverConfig()
		cfg.FilterSource = ""
		ragMgr.UpdateRetrieverConfig(cfg)
	}

	// 转换结果
	searchResults := make([]map[string]interface{}, len(results))
	for i, res := range results {
		searchResults[i] = map[string]interface{}{
			"chunk_id":   res.ChunkID,
			"content":    res.Content,
			"score":      res.Score,
			"doc_title":  res.DocTitle,
			"doc_source": res.DocSource,
			"metadata":   res.Metadata,
		}
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"query":   req.Query,
		"count":   len(searchResults),
		"results": searchResults,
	})
}

// handleRAGStats 返回 RAG 知识库统计信息
func (s *Server) handleRAGStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	ragMgr := s.agent.RAG()
	if ragMgr == nil {
		s.sendError(w, "RAG not initialized", http.StatusServiceUnavailable, "")
		return
	}

	stats := ragMgr.Stats()
	docIDs := ragMgr.ListDocuments()

	docs := make([]map[string]interface{}, 0, len(docIDs))
	for _, id := range docIDs {
		if doc, ok := ragMgr.GetDocument(id); ok {
			docs = append(docs, map[string]interface{}{
				"id":         doc.ID,
				"title":      doc.Title,
				"path":       doc.Path,
				"chunks":     len(doc.Chunks),
				"indexed_at": doc.IndexedAt,
			})
		}
	}

	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"document_count": stats.DocumentCount,
		"chunk_count":    stats.ChunkCount,
		"total_tokens":   stats.TotalTokens,
		"last_indexed":   stats.LastIndexed,
		"sources":        stats.Sources,
		"documents":      docs,
		"retriever": map[string]interface{}{
			"top_k":      ragMgr.RetrieverConfig().TopK,
			"min_score":  ragMgr.RetrieverConfig().MinScore,
			"use_mmr":    ragMgr.RetrieverConfig().UseMMR,
			"mmr_lambda": ragMgr.RetrieverConfig().MMRLambda,
		},
	})
}

// --- v0.16.0: Function Calling API ---

// handleFunctionCalling 处理 /api/v1/fc 请求
// POST: 执行 function calling
// GET: 获取 function calling 状态
func (s *Server) handleFunctionCalling(w http.ResponseWriter, r *http.Request) {
	s.dispatchMethod(w, r, map[string]func(){
		http.MethodGet: func() {
			s.sendJSON(w, http.StatusOK, map[string]any{
				"version":     "0.20.0",
				"description": "OpenAI Function Calling support",
				"endpoints": map[string]string{
					"POST /api/v1/fc":         "Execute function calling",
					"GET  /api/v1/fc/tools":   "List available function tools",
					"GET  /api/v1/fc/history": "Get function call history",
				},
			})
		},
		http.MethodPost: func() {
			var req fcRequest
			if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
				s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
				return
			}

			if req.Message == "" {
				s.sendError(w, "message is required", http.StatusBadRequest, "")
				return
			}

			loopCfg := agent.DefaultLoopConfig()
			if s.agent != nil && s.agent.Config() != nil {
				cfg := s.agent.Config().Get()
				agent.ApplyAgentLoopConfig(&loopCfg, cfg.Agent)
			}
			loopCfg.AutoApprove = req.AutoApprove
			if req.MaxIter > 0 {
				loopCfg.MaxIterations = req.MaxIter
			}

			start := time.Now()
			result, err := s.agent.RunLoop(r.Context(), req.Message, loopCfg)
			if err != nil {
				s.sendError(w, "function calling failed", http.StatusInternalServerError, err.Error())
				return
			}

			duration := time.Since(start)
			resp := fcResponse{
				Response:   result.Response,
				Iterations: result.Iterations,
				TokensUsed: result.TokensUsed,
				Duration:   duration.String(),
				State:      result.State.String(),
			}

			for _, tc := range result.ToolCalls {
				resp.ToolCalls = append(resp.ToolCalls, toolCallInfo{
					Name:      tc.Name,
					Arguments: tc.Arguments,
					Result:    tc.Result,
					Duration:  tc.Duration.String(),
				})
			}

			s.sendJSON(w, http.StatusOK, resp)
		},
	})
}

// handleFCTools 列出可用的 function calling 工具
func (s *Server) handleFCTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	tools := s.agent.Tools().ListEnabled()
	type fcToolInfo struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
		Permission  string         `json:"permission"`
		Category    string         `json:"category"`
	}

	var infos []fcToolInfo
	for _, t := range tools {
		openaiFmt := t.ToOpenAIFormat()
		var params map[string]any
		if fn, ok := openaiFmt["function"].(map[string]any); ok {
			params, _ = fn["parameters"].(map[string]any)
		}
		infos = append(infos, fcToolInfo{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
			Permission:  t.Permission.String(),
			Category:    string(t.Category),
		})
	}

	s.sendJSON(w, http.StatusOK, map[string]any{
		"tools": infos,
		"count": len(infos),
	})
}

// handleFCHistory 获取 function calling 历史
func (s *Server) handleFCHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}

	// Function calling 历史由 Agent 内部管理
	// 这里返回最近一次 loop 的工具调用信息
	s.sendJSON(w, http.StatusOK, map[string]any{
		"message": "Function call history is managed per-session. Use /api/v1/sessions for session history.",
	})
}

// fcRequest 是 function calling 请求
type fcRequest struct {
	Message     string `json:"message"`
	AutoApprove bool   `json:"auto_approve,omitempty"`
	MaxIter     int    `json:"max_iterations,omitempty"`
}

// fcResponse 是 function calling 响应
type fcResponse struct {
	Response   string         `json:"response"`
	Iterations int            `json:"iterations"`
	TokensUsed int            `json:"tokens_used"`
	ToolCalls  []toolCallInfo `json:"tool_calls,omitempty"`
	Duration   string         `json:"duration"`
	State      string         `json:"state"`
}

// --- v0.21.0: RAG 持久化存储 API ---

// handleRAGStore 管理 RAG 向量存储后端
func (s *Server) handleRAGStore(w http.ResponseWriter, r *http.Request) {
	ragMgr := s.agent.RAG()
	if ragMgr == nil {
		s.sendError(w, "RAG not initialized", http.StatusServiceUnavailable, "")
		return
	}

	s.dispatchMethod(w, r, map[string]func(){
		http.MethodGet: func() {
			// 获取存储后端信息
			result := map[string]interface{}{
				"backend": "memory",
				"sqlite":  false,
			}

			if ragMgr.IsSQLite() {
				sqlStore := ragMgr.SQLiteStore()
				count, dbSize, err := sqlStore.Stats()
				if err != nil {
					s.sendError(w, "failed to get sqlite stats", http.StatusInternalServerError, err.Error())
					return
				}
				result = map[string]interface{}{
					"backend":   "sqlite",
					"sqlite":    true,
					"db_path":   sqlStore.Path(),
					"entries":   count,
					"db_size":   dbSize,
					"dimension": sqlStore.Dimension(),
				}
			} else {
				store := ragMgr.Store()
				result = map[string]interface{}{
					"backend":   "memory",
					"sqlite":    false,
					"entries":   store.Len(),
					"dimension": store.Dimension(),
				}
			}

			s.sendJSON(w, http.StatusOK, result)
		},
		http.MethodPost: func() {
			// 迁移到 SQLite 后端
			var req struct {
				DBPath string `json:"db_path,omitempty"` // 可选自定义路径
			}
			if err := jsonAPI.NewDecoder(r.Body).Decode(&req); err != nil {
				s.sendError(w, "invalid request body", http.StatusBadRequest, err.Error())
				return
			}

			if ragMgr.IsSQLite() {
				s.sendError(w, "already using SQLite backend", http.StatusConflict, "")
				return
			}

			// 迁移逻辑由 Agent 层处理
			s.sendJSON(w, http.StatusOK, map[string]interface{}{
				"message": "migration to SQLite requires restart with SQLite backend enabled",
				"hint":    "SQLite backend is enabled by default in v0.21.0+",
			})
		},
	})
}
