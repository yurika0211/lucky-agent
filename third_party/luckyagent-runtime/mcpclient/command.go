package mcpclient

import (
	"context"
	"fmt"
	"strings"
)

const CommandUsage = "用法：/mcp status | connect <配置名> | refresh <名称> | disconnect <名称> | <名称> <URL> [api_key]"

// RunCommand is shared by CLI and gateways. It depends only on the management
// port and never constructs a transport or touches a tool registry.
func RunCommand(ctx context.Context, admin Admin, argument string) (string, error) {
	if admin == nil {
		return "", fmt.Errorf("MCP runtime unavailable")
	}
	parts := strings.Fields(argument)
	if len(parts) == 0 || (len(parts) == 1 && parts[0] == "status") {
		statuses := admin.Status()
		if len(statuses) == 0 {
			return "没有 MCP 服务；在 mcp.servers 中配置，或使用 /mcp <名称> <URL> 临时连接。", nil
		}
		var lines []string
		for _, s := range statuses {
			lines = append(lines, fmt.Sprintf("%s: %s (%s, %d tools) %s", s.Name, s.State, s.Transport, s.ToolCount, s.Error))
		}
		return strings.Join(lines, "\n"), nil
	}
	if len(parts) < 2 {
		return "", fmt.Errorf("%s", CommandUsage)
	}
	var err error
	switch parts[0] {
	case "connect", "refresh", "disconnect":
		if len(parts) != 2 {
			return "", fmt.Errorf("%s", CommandUsage)
		}
		switch parts[0] {
		case "connect":
			err = admin.ConnectConfigured(ctx, parts[1])
		case "refresh":
			err = admin.Refresh(ctx, parts[1])
		case "disconnect":
			err = admin.Disconnect(ctx, parts[1])
		}
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("MCP %s: %s", parts[0], parts[1]), nil
	default:
		if len(parts) > 3 {
			return "", fmt.Errorf("%s", CommandUsage)
		}
		cfg := ServerConfig{Transport: "http", URL: parts[1]}
		if len(parts) == 3 {
			cfg.APIKey = parts[2]
		}
		if err = admin.Connect(ctx, parts[0], cfg); err != nil {
			return "", err
		}
		return fmt.Sprintf("已连接 MCP 服务：%s（临时配置）", parts[0]), nil
	}
}
