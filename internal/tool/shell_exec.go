package tool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func buildShellCommand(command string) (*exec.Cmd, error) {
	if runtime.GOOS != "windows" {
		return exec.Command("sh", "-c", command), nil
	}

	powerShellPath, err := resolvePowerShellBinary()
	if err != nil {
		return nil, err
	}
	return exec.Command(powerShellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command), nil
}

func buildScriptCommand(scriptPath string, args ...string) ([]string, error) {
	ext := strings.ToLower(filepath.Ext(scriptPath))

	if runtime.GOOS == "windows" {
		switch ext {
		case ".py":
			pythonPath, err := resolveBinary("python", "python3")
			if err != nil {
				return nil, err
			}
			return append([]string{pythonPath, scriptPath}, args...), nil
		case ".js":
			nodePath, err := resolveBinary("node")
			if err != nil {
				return nil, err
			}
			return append([]string{nodePath, scriptPath}, args...), nil
		default:
			return buildWindowsBashScriptCommand(scriptPath, args...)
		}
	}

	switch ext {
	case ".sh":
		return append([]string{"sh", scriptPath}, args...), nil
	case ".py":
		return append([]string{"python3", scriptPath}, args...), nil
	case ".js":
		return append([]string{"node", scriptPath}, args...), nil
	default:
		return append([]string{"sh", scriptPath}, args...), nil
	}
}

func buildWindowsBashScriptCommand(scriptPath string, args ...string) ([]string, error) {
	bashPath, err := resolveBashBinary()
	if err != nil {
		return nil, err
	}

	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(toBashPath(scriptPath)))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return []string{bashPath, "-lc", strings.Join(parts, " ")}, nil
}

func resolveBashBinary() (string, error) {
	return resolveBinary(
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files\Git\usr\bin\bash.exe`,
		`C:\Windows\system32\bash.exe`,
		"bash.exe",
		"bash",
	)
}

func resolvePowerShellBinary() (string, error) {
	return resolveBinary(
		"pwsh.exe",
		"pwsh",
		`C:\Program Files\PowerShell\7\pwsh.exe`,
		`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
		"powershell.exe",
		"powershell",
	)
}

func resolveBinary(candidates ...string) (string, error) {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if strings.Contains(candidate, `\`) || strings.Contains(candidate, "/") {
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("unable to locate executable from candidates: %s", strings.Join(candidates, ", "))
}

func toBashPath(path string) string {
	clean := filepath.Clean(path)
	slash := filepath.ToSlash(clean)
	if len(slash) >= 2 && slash[1] == ':' {
		drive := strings.ToLower(string(slash[0]))
		rest := strings.TrimPrefix(slash[2:], "/")
		if rest == "" {
			return "/" + drive
		}
		return "/" + drive + "/" + rest
	}
	return slash
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
