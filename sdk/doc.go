// Package sdk is the embeddable LuckyAgent runtime surface.
//
// It lets other Go programs import LuckyAgent and run an agent in-process
// without starting `lh serve` or speaking HTTP/gRPC.
//
// Stability contract (v0):
//   - Prefer symbols in this package only.
//   - Do not import github.com/yurika0211/luckyagent/internal/... from app code.
//   - Breaking changes will be avoided on exported names; behavior may still
//     track the main runtime until a tagged sdk/v1 split.
//
// v0 surface:
//   - Lifecycle: New, Close, HomeDir
//   - Chat: Chat, ChatSession, ChatStream, ChatSessionStream
//   - Sessions: NewSession, NewSessionWithTitle, ListSessions, GetSession,
//     RenameSession, DeleteSession
//   - Memory: Remember, RememberLongTerm, Recall
//   - RAG: IndexText, IndexFile, IndexDirectory, SearchRAG, RemoveDocument,
//     ListDocuments, RAGStats
//   - Tools: RegisterTool, UnregisterTool, EnableTool, DisableTool, ListTools
//   - Model: SwitchModel, CurrentModel, ListModels
//
// Cancellation is done by canceling the context passed to Chat* / RAG methods.
//
// Minimal usage:
//
//	agent, err := sdk.New(sdk.Config{
//		HomeDir:  "/tmp/my-agent-home",
//		Provider: "openai",
//		Model:    "gpt-5.4-mini",
//		APIKey:   os.Getenv("OPENAI_API_KEY"),
//	})
//	if err != nil { ... }
//	defer agent.Close()
//
//	out, err := agent.Chat(ctx, "hello")
package sdk
