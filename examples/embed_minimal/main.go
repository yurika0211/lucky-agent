package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/yurika0211/luckyagent/sdk"
)

func main() {
	home := filepath.Join(os.TempDir(), "luckyagent-embed-example")
	_ = os.MkdirAll(home, 0o700)

	yes := true
	agent, err := sdk.New(sdk.Config{
		HomeDir:      home,
		Provider:     "openai",
		Model:        "gpt-5.4-mini",
		APIKey:       os.Getenv("OPENAI_API_KEY"),
		SystemPrompt: "You are a minimal embed-sdk demo agent. Keep replies short.",
		AutoApprove:  &yes,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer agent.Close()

	_ = agent.RegisterTool(sdk.ToolSpec{
		Name:        "embed_ping",
		Description: "Return pong for embed demo",
		AutoApprove: true,
		Handler: func(args map[string]any) (string, error) {
			return "pong", nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// One-shot
	reply, err := agent.Chat(ctx, "Reply with exactly: embed-sdk-ok")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("chat:", reply)

	// Multi-turn + stream
	sid, events, err := agent.ChatStream(ctx, "Count to 3, briefly.")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("session:", sid)
	for ev := range events {
		switch ev.Type {
		case sdk.EventContent:
			fmt.Print(ev.Content)
		case sdk.EventDone:
			fmt.Println()
			fmt.Println("done:", ev.Content)
		case sdk.EventError:
			log.Fatal(ev.Err)
		}
	}
}
