package soul

import (
	"fmt"
	"os"
	"strings"
)

// Soul 代表 Agent 的人格定义
type Soul struct {
	Content  string
	FilePath string
}

// Load 从文件加载 SOUL.md
func Load(path string) (*Soul, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read soul file: %w", err)
	}
	return &Soul{
		Content:  string(data),
		FilePath: path,
	}, nil
}

// Default 创建默认 Soul
func Default() *Soul {
	return &Soul{
		Content: `You are LuckyAgent Agent, an intelligent AI assistant.
You are helpful, concise, and direct.
Answer in the user's language by default.
Provide code examples when relevant.
Admit when you don't know something.`,
	}
}

// SystemPrompt 生成系统提示词
func (s *Soul) SystemPrompt() string {
	return strings.TrimSpace(s.Content)
}

// Reload 重新加载 SOUL.md
func (s *Soul) Reload() error {
	if s.FilePath == "" {
		return fmt.Errorf("no file path set")
	}
	data, err := os.ReadFile(s.FilePath)
	if err != nil {
		return fmt.Errorf("reload soul: %w", err)
	}
	s.Content = string(data)
	return nil
}
