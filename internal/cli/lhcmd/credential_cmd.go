package lhcmd

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/yurika0211/luckyagent/internal/credentials"
)

func runCredentialAdd(cmdArgs credentialCommandArgs) error {
	value, err := readCredentialValue()
	if err != nil {
		return err
	}
	defer clearBytes(value)

	store, err := credentials.NewStore(cmdArgs.homeDir)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.PutBytes(context.Background(), cmdArgs.id, cmdArgs.kind, cmdArgs.scope, value); err != nil {
		return err
	}
	fmt.Printf("凭据已保存: %s\n", cmdArgs.id)
	return nil
}

func runCredentialList(homeDir string) error {
	store, err := credentials.NewStore(homeDir)
	if err != nil {
		return err
	}
	defer store.Close()
	items, err := store.List(context.Background())
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("暂无凭据")
		return nil
	}
	fmt.Printf("%-24s %-18s %-14s %s\n", "ID", "KIND", "SCOPE", "UPDATED")
	for _, item := range items {
		fmt.Printf("%-24s %-18s %-14s %s\n", item.ID, item.Kind, item.Scope, item.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	}
	return nil
}

func runCredentialRemove(homeDir, id string) error {
	store, err := credentials.NewStore(homeDir)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Delete(context.Background(), id); err != nil {
		return err
	}
	fmt.Printf("凭据已删除: %s\n", id)
	return nil
}

type credentialCommandArgs struct {
	homeDir string
	id      string
	kind    string
	scope   string
}

func readCredentialValue() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("credential input requires an interactive TTY")
	}
	fmt.Fprint(os.Stderr, "Credential value: ")
	value, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("read credential value: %w", err)
	}
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		clearBytes(value)
		return nil, fmt.Errorf("credential value is required")
	}
	valueCopy := append([]byte(nil), trimmed...)
	clearBytes(value)
	return valueCopy, nil
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
