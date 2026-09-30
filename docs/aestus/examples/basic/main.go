package main

import (
	"fmt"
	"log"
	"os"

	"github.com/yurika0211/luckyagent/internal/memory"
)

func main() {
	dir := os.Getenv("AESTUS_DIR")
	if dir == "" {
		dir = "./.aestus/memory"
	}

	store, err := memory.NewStore(dir)
	if err != nil {
		log.Fatal(err)
	}

	if err := store.SaveWithMetadata(
		"Aestus stores durable memories as Obsidian-compatible Markdown.",
		"project",
		memory.TierLong,
		0.9,
		[]string{"aestus", "memory"},
		[]string{"Obsidian"},
		[]string{"file-first memory"},
	); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("vault: %s\n", store.Dir())
	for _, entry := range store.Search("Obsidian memory") {
		fmt.Printf("- [%s/%s] %s\n", entry.Category, entry.Tier, entry.Content)
	}
}
