package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Metadata represents the YAML frontmatter of a prompt file
type Metadata struct {
	ID          string            `yaml:"id"`
	Version     string            `yaml:"version"`
	Description string            `yaml:"description"`
	Language    string            `yaml:"language,omitempty"`
	Variables   map[string]string `yaml:"variables,omitempty"`
}

// Prompt represents a loaded prompt with its metadata
type Prompt struct {
	Meta    Metadata
	Content string
}

// cachedPrompt stores a prompt with its file modification time
type cachedPrompt struct {
	prompt  Prompt
	modTime time.Time
}

// Loader manages loading and caching of external prompt files
type Loader struct {
	baseDir string
	cache   sync.Map // map[string]cachedPrompt
	mu      sync.RWMutex
}

// NewLoader creates a new prompt loader with the specified base directory
func NewLoader(baseDir string) *Loader {
	return &Loader{
		baseDir: baseDir,
	}
}

// Load loads a prompt from a file path relative to the base directory
// Example: Load("core.md") or Load("platform/telegram.md")
func (l *Loader) Load(relativePath string) (Prompt, error) {
	fullPath := filepath.Join(l.baseDir, relativePath)

	// Check cache first
	if cached, ok := l.getCached(fullPath); ok {
		// Check if file has been modified
		stat, err := os.Stat(fullPath)
		if err == nil && stat.ModTime().Equal(cached.modTime) {
			return cached.prompt, nil
		}
	}

	// Load from file
	prompt, err := l.loadFromFile(fullPath)
	if err != nil {
		return Prompt{}, err
	}

	// Cache it
	stat, _ := os.Stat(fullPath)
	l.cache.Store(fullPath, cachedPrompt{
		prompt:  prompt,
		modTime: stat.ModTime(),
	})

	return prompt, nil
}

// LoadOrDefault loads a prompt, returning the default content if file doesn't exist
func (l *Loader) LoadOrDefault(relativePath string, defaultContent string) string {
	prompt, err := l.Load(relativePath)
	if err != nil {
		return defaultContent
	}
	return prompt.Content
}

// LoadWithVars loads a prompt and replaces template variables
func (l *Loader) LoadWithVars(relativePath string, vars map[string]string) (string, error) {
	prompt, err := l.Load(relativePath)
	if err != nil {
		return "", err
	}

	content := prompt.Content
	for key, value := range vars {
		placeholder := fmt.Sprintf("{{%s}}", key)
		content = strings.ReplaceAll(content, placeholder, value)
	}

	return content, nil
}

// getCached retrieves a cached prompt if it exists
func (l *Loader) getCached(fullPath string) (cachedPrompt, bool) {
	val, ok := l.cache.Load(fullPath)
	if !ok {
		return cachedPrompt{}, false
	}
	cached, ok := val.(cachedPrompt)
	return cached, ok
}

// loadFromFile reads and parses a prompt file
func (l *Loader) loadFromFile(fullPath string) (Prompt, error) {
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return Prompt{}, fmt.Errorf("failed to read prompt file %s: %w", fullPath, err)
	}

	content := string(data)

	// Parse YAML frontmatter
	meta, body, err := parseFrontmatter(content)
	if err != nil {
		// If no frontmatter, use entire content as body
		return Prompt{
			Meta:    Metadata{},
			Content: strings.TrimSpace(content),
		}, nil
	}

	return Prompt{
		Meta:    meta,
		Content: strings.TrimSpace(body),
	}, nil
}

// parseFrontmatter extracts YAML frontmatter and body content
func parseFrontmatter(content string) (Metadata, string, error) {
	content = strings.TrimSpace(content)

	if !strings.HasPrefix(content, "---") {
		return Metadata{}, "", fmt.Errorf("no frontmatter found")
	}

	// Find the closing ---
	rest := content[3:]
	endIdx := strings.Index(rest, "\n---")
	if endIdx == -1 {
		return Metadata{}, "", fmt.Errorf("unclosed frontmatter")
	}

	frontmatterStr := rest[:endIdx]
	body := rest[endIdx+4:] // Skip "\n---"

	var meta Metadata
	if err := yaml.Unmarshal([]byte(frontmatterStr), &meta); err != nil {
		return Metadata{}, "", fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	return meta, body, nil
}

// Exists checks if a prompt file exists
func (l *Loader) Exists(relativePath string) bool {
	fullPath := filepath.Join(l.baseDir, relativePath)
	_, err := os.Stat(fullPath)
	return err == nil
}

// ClearCache clears the entire prompt cache
func (l *Loader) ClearCache() {
	l.cache.Range(func(key, value interface{}) bool {
		l.cache.Delete(key)
		return true
	})
}
