package soul

import (
	"embed"
	"fmt"
	"strings"
	"sync"
)

//go:embed templates/*.md
var templateFS embed.FS

// Template 代表一个 SOUL 模板
type Template struct {
	ID          string            `yaml:"id" json:"id"`
	Name        string            `yaml:"name" json:"name"`
	Language    string            `yaml:"language" json:"language"`       // zh, en, ja, ko 等
	Description string            `yaml:"description" json:"description"`
	Content     string            `yaml:"content" json:"content"`
	Variables   map[string]string  `yaml:"variables,omitempty" json:"variables,omitempty"`
	Tags        []string          `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// TemplateManager 管理 SOUL 模板
type TemplateManager struct {
	mu        sync.RWMutex
	templates map[string]*Template
	builtin   map[string]*Template // 内置模板
}

// NewTemplateManager 创建模板管理器
func NewTemplateManager() *TemplateManager {
	tm := &TemplateManager{
		templates: make(map[string]*Template),
		builtin:   make(map[string]*Template),
	}
	tm.loadBuiltinTemplates()
	// 内置模板也加入 templates
	for k, v := range tm.builtin {
		tm.templates[k] = v
	}
	return tm
}

// loadBuiltinTemplates 从嵌入的文件系统加载内置模板
func (tm *TemplateManager) loadBuiltinTemplates() {
	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := templateFS.ReadFile("templates/" + entry.Name())
		if err != nil {
			continue
		}
		tmpl := parseTemplate(string(data))
		if tmpl != nil {
			tm.builtin[tmpl.ID] = tmpl
		}
	}
}

// parseTemplate 解析模板文件
// 格式: 第一行是 --- 分隔的 YAML frontmatter，后面是内容
func parseTemplate(raw string) *Template {
	tmpl := &Template{
		Variables: make(map[string]string),
		Tags:      []string{},
	}

	// 检查是否有 frontmatter
	if !strings.HasPrefix(raw, "---") {
		// 无 frontmatter，整个内容作为默认模板
		tmpl.ID = "default"
		tmpl.Name = "Default"
		tmpl.Language = "en"
		tmpl.Content = strings.TrimSpace(raw)
		return tmpl
	}

	// 解析 frontmatter
	parts := strings.SplitN(raw, "---", 3)
	if len(parts) < 3 {
		return nil
	}

	frontmatter := strings.TrimSpace(parts[1])
	content := strings.TrimSpace(parts[2])

	// 简单 YAML 解析（不引入额外依赖）
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx == -1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

		switch key {
		case "id":
			tmpl.ID = value
		case "name":
			tmpl.Name = value
		case "language":
			tmpl.Language = value
		case "description":
			tmpl.Description = value
		case "tags":
			tags := strings.Split(value, ",")
			for i, t := range tags {
				tags[i] = strings.TrimSpace(t)
			}
			tmpl.Tags = tags
		}
	}

	if tmpl.ID == "" {
		tmpl.ID = "default"
	}

	tmpl.Content = content
	return tmpl
}

// ListTemplates 列出所有可用模板
func (tm *TemplateManager) ListTemplates() []*Template {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make([]*Template, 0, len(tm.templates))
	for _, t := range tm.templates {
		result = append(result, t)
	}
	return result
}

// ListByLanguage 按语言列出模板
func (tm *TemplateManager) ListByLanguage(lang string) []*Template {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	result := make([]*Template, 0)
	for _, t := range tm.templates {
		if t.Language == lang {
			result = append(result, t)
		}
	}
	return result
}

// GetTemplate 获取指定模板
func (tm *TemplateManager) GetTemplate(id string) (*Template, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	t, ok := tm.templates[id]
	if !ok {
		return nil, fmt.Errorf("template %q not found", id)
	}
	return t, nil
}

// AddTemplate 添加自定义模板
func (tm *TemplateManager) AddTemplate(tmpl *Template) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tmpl.ID == "" {
		return fmt.Errorf("template ID is required")
	}
	tm.templates[tmpl.ID] = tmpl
	return nil
}

// RemoveTemplate 移除自定义模板（不能移除内置模板）
func (tm *TemplateManager) RemoveTemplate(id string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, ok := tm.builtin[id]; ok {
		return fmt.Errorf("cannot remove builtin template %q", id)
	}
	if _, ok := tm.templates[id]; !ok {
		return fmt.Errorf("template %q not found", id)
	}
	delete(tm.templates, id)
	return nil
}

// Render 渲染模板，替换变量
func (t *Template) Render(vars map[string]string) string {
	content := t.Content

	// 先用模板自带默认值
	allVars := make(map[string]string)
	for k, v := range t.Variables {
		allVars[k] = v
	}
	// 用户传入的变量覆盖默认值
	for k, v := range vars {
		allVars[k] = v
	}

	// 替换 {{variable}} 格式的变量
	for k, v := range allVars {
		content = strings.ReplaceAll(content, "{{"+k+"}}", v)
	}

	return content
}

// DetectLanguage 从用户输入检测语言
func DetectLanguage(text string) string {
	// 简单的基于 Unicode 范围的语言检测
	var zh, ja, ko, latin int
	for _, r := range text {
		switch {
		case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
			zh++
		case r >= 0x3040 && r <= 0x309F: // Hiragana
			ja++
		case r >= 0x30A0 && r <= 0x30FF: // Katakana
			ja++
		case r >= 0xAC00 && r <= 0xD7AF: // Hangul Syllables
			ko++
		case r >= 0x0041 && r <= 0x007A || r >= 0x00C0 && r <= 0x024F: // Latin
			latin++
		}
	}

	// 日文优先（因为有平假名/片假名）
	if ja > 0 {
		return "ja"
	}
	if ko > zh && ko > latin {
		return "ko"
	}
	if zh > latin {
		return "zh"
	}
	return "en"
}

// SupportedLanguages 返回支持的语言列表
func SupportedLanguages() []string {
	return []string{"zh", "en", "ja", "ko"}
}

// LanguageName 返回语言的全名
func LanguageName(code string) string {
	names := map[string]string{
		"zh": "中文",
		"en": "English",
		"ja": "日本語",
		"ko": "한국어",
	}
	if name, ok := names[code]; ok {
		return name
	}
	return code
}
