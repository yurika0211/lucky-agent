package lhcmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyagent/internal/config"
)

func addTUICmd(root *cobra.Command) {
	var uiDir string

	tuiCmd := &cobra.Command{
		Use:   "tui",
		Short: "启动 LuckyAgent 终端 UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			apiBase, _ := cmd.Flags().GetString("api-base")
			session, _ := cmd.Flags().GetString("session")
			model, _ := cmd.Flags().GetString("model")
			return runTUI(uiDir, apiBase, session, model)
		},
	}
	tuiCmd.Flags().StringVar(&uiDir, "ui-dir", "", "UI workspace path; defaults to LH_TUI_DIR/LH_UI_DIR or auto-detection")
	tuiCmd.Flags().String("api-base", "http://127.0.0.1:9090", "LuckyAgent API base URL")
	tuiCmd.Flags().String("session", "dashboard-main", "TUI session id")
	tuiCmd.Flags().String("model", "", "model label override")
	root.AddCommand(tuiCmd)
}

/**
 * 启动TUI的函数，带ui的目录，url还有session的id,最后还有启动的模型
 */
func runTUI(uiDir, apiBase, session, model string) error {
	resolved, err := resolveUIWorkspace(uiDir)
	if err != nil {
		return err
	}
	if !isInteractiveTerminal() {
		return fmt.Errorf("TUI requires an interactive terminal")
	}

	if err := ensureServeRunning(apiBase); err != nil {
		return err
	}

	node, err := resolveTUIRuntime(resolved)
	if err != nil {
		return err
	}
	args, err := tuiEntrypointArgs(resolved)
	if err != nil {
		return err
	}
	args = append(args, "--api-base", apiBase, "--session", session)
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.Command(node, args...)
	cmd.Dir = resolved
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

/**
 * resolve UI Workspace
 */
func resolveUIWorkspace(explicit string) (string, error) {
	candidates := tuiDirCandidates(explicit)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		resolved, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if isUIWorkspace(resolved) {
			return resolved, nil
		}
		if isUIWorkspace(filepath.Join(resolved, "UI")) {
			return filepath.Join(resolved, "UI"), nil
		}
	}
	return "", fmt.Errorf("could not locate LuckyAgent UI workspace; pass --ui-dir or set LH_TUI_DIR to the repo UI directory")
}

func tuiDirCandidates(explicit string) []string {
	candidates := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			candidates = append(candidates, value)
		}
	}

	add(explicit)
	add(os.Getenv("LH_TUI_DIR"))
	add(os.Getenv("LH_UI_DIR"))
	add(readSavedTUIDir())

	if cwd, err := os.Getwd(); err == nil {
		add(cwd)
		for _, parent := range walkParents(cwd, 8) {
			add(parent)
			add(filepath.Join(parent, "UI"))
		}
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		add(exeDir)
		for _, parent := range walkParents(exeDir, 8) {
			add(parent)
			add(filepath.Join(parent, "UI"))
			add(filepath.Join(parent, "luckyagent"))
			add(filepath.Join(parent, "luckyagent", "UI"))
		}
	}

	return candidates
}

func readSavedTUIDir() string {
	mgr, err := config.NewManager()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(mgr.HomeDir(), "runtime", "tui-ui-dir"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func walkParents(start string, maxDepth int) []string {
	out := []string{}
	current, err := filepath.Abs(start)
	if err != nil {
		return out
	}
	for depth := 0; depth < maxDepth; depth++ {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		out = append(out, parent)
		current = parent
	}
	return out
}

func isUIWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "TUI", "dist", "tui.mjs")); err == nil {
		return true
	}
	for _, path := range []string{
		filepath.Join(dir, "package.json"),
		filepath.Join(dir, "TUI", "package.json"),
		filepath.Join(dir, "TUI", "src", "index.tsx"),
	} {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

func tuiEntrypointArgs(uiDir string) ([]string, error) {
	bundle := filepath.Join(uiDir, "TUI", "dist", "tui.mjs")
	if _, err := os.Stat(bundle); err == nil {
		return []string{bundle}, nil
	}
	source := filepath.Join(uiDir, "TUI", "src", "index.tsx")
	if _, err := os.Stat(source); err == nil {
		return []string{"--import", "tsx", "TUI/src/index.tsx"}, nil
	}
	return nil, fmt.Errorf("could not locate LuckyAgent TUI entrypoint in %s", uiDir)
}

func resolveTUIRuntime(uiDir string) (string, error) {
	if value := strings.TrimSpace(os.Getenv("LH_NODE_BINARY")); value != "" {
		if _, err := os.Stat(value); err == nil {
			return value, nil
		}
		return "", fmt.Errorf("LH_NODE_BINARY does not exist: %s", value)
	}

	nodeName := "node"
	if runtime.GOOS == "windows" {
		nodeName = "node.exe"
	}
	for _, candidate := range []string{
		filepath.Join(uiDir, "..", "runtime", "node", "bin", nodeName),
		filepath.Join(uiDir, "..", "runtime", "node", nodeName),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if node, err := exec.LookPath(nodeName); err == nil {
		return node, nil
	}
	return "", fmt.Errorf("could not locate Node.js runtime; install a LuckyAgent application package or set LH_NODE_BINARY")
}

func isInteractiveTerminal() bool {
	stdin, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return stdin.Mode()&os.ModeCharDevice != 0
}
