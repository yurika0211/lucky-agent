package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Profile 代表一个独立的 LuckyHarness 实例配置
type Profile struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Provider    string            `yaml:"provider"`
	APIKey      string            `yaml:"api_key,omitempty"`
	APIBase     string            `yaml:"api_base,omitempty"`
	Model       string            `yaml:"model"`
	SoulPath    string            `yaml:"soul_path,omitempty"`
	MaxTokens   int               `yaml:"max_tokens"`
	Temperature float64           `yaml:"temperature"`
	Env         map[string]string `yaml:"env,omitempty"`
	Fallbacks   []FallbackEntry   `yaml:"fallbacks,omitempty"`
}

// FallbackEntry 降级链节点
type FallbackEntry struct {
	Provider string `yaml:"provider"`
	APIKey   string `yaml:"api_key,omitempty"`
	APIBase  string `yaml:"api_base,omitempty"`
	Model    string `yaml:"model,omitempty"`
}

// DefaultProfile 返回默认 profile
func DefaultProfile(name string) *Profile {
	return &Profile{
		Name:        name,
		Description: "Default LuckyHarness profile",
		Provider:    "openai",
		Model:       "gpt-5.4-mini",
		MaxTokens:   40960,
		Temperature: 0.7,
		Env:         make(map[string]string),
	}
}

// Manager 管理多个 Profile
type Manager struct {
	mu         sync.RWMutex
	homeDir    string // ~/.luckyharness
	activeName string // 当前活跃 profile 名称
	profiles   map[string]*Profile
}

// NewManager 创建 Profile 管理器
func NewManager(homeDir string) (*Manager, error) {
	m := &Manager{
		homeDir:  homeDir,
		profiles: make(map[string]*Profile),
	}

	// 确保目录存在
	profilesDir := filepath.Join(homeDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		return nil, fmt.Errorf("create profiles dir: %w", err)
	}

	// 加载所有 profile
	if err := m.loadAll(); err != nil {
		return nil, fmt.Errorf("load profiles: %w", err)
	}

	// 加载活跃 profile 标记
	activeFile := filepath.Join(homeDir, ".active_profile")
	data, err := os.ReadFile(activeFile)
	if err == nil {
		m.activeName = strings.TrimSpace(string(data))
	} else {
		// 默认使用 "default"
		m.activeName = "default"
	}

	// 如果 default 不存在，创建它
	if _, ok := m.profiles["default"]; !ok {
		def := DefaultProfile("default")
		m.profiles["default"] = def
		_ = m.saveProfile(def)
	}

	// 如果活跃 profile 不存在，回退到 default
	if _, ok := m.profiles[m.activeName]; !ok {
		m.activeName = "default"
	}

	return m, nil
}

// loadAll 加载所有 profile 文件
func (m *Manager) loadAll() error {
	profilesDir := filepath.Join(m.homeDir, "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read profiles dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".yaml")
		path := filepath.Join(profilesDir, entry.Name())

		data, err := os.ReadFile(path)
		if err != nil {
			continue // 跳过无法读取的文件
		}

		var p Profile
		if err := yaml.Unmarshal(data, &p); err != nil {
			continue // 跳过格式错误的文件
		}

		p.Name = name // 文件名作为 profile 名
		m.profiles[name] = &p
	}

	return nil
}

// saveProfile 保存单个 profile 到磁盘
func (m *Manager) saveProfile(p *Profile) error {
	profilesDir := filepath.Join(m.homeDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}

	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}

	path := filepath.Join(profilesDir, p.Name+".yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write profile: %w", err)
	}

	return nil
}

// Create 创建新 profile
func (m *Manager) Create(name, description string) (*Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.profiles[name]; ok {
		return nil, fmt.Errorf("profile %q already exists", name)
	}

	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		return nil, fmt.Errorf("invalid profile name: %q", name)
	}

	p := DefaultProfile(name)
	p.Description = description
	m.profiles[name] = p

	if err := m.saveProfile(p); err != nil {
		delete(m.profiles, name)
		return nil, err
	}

	// 初始化 profile 数据目录
	profileDir := m.profileDataDir(name)
	for _, sub := range []string{"sessions", "memory", "logs", "skills"} {
		if err := os.MkdirAll(filepath.Join(profileDir, sub), 0o700); err != nil {
			return nil, fmt.Errorf("create %s: %w", sub, err)
		}
	}

	return p, nil
}

// Get 获取指定 profile
func (m *Manager) Get(name string) (*Profile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.profiles[name]
	if !ok {
		return nil, fmt.Errorf("profile %q not found", name)
	}
	cp := *p
	return &cp, nil
}

// Active 获取当前活跃 profile
func (m *Manager) Active() *Profile {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.profiles[m.activeName]
	if !ok {
		// 回退
		p = m.profiles["default"]
	}
	if p == nil {
		return DefaultProfile("default")
	}
	cp := *p
	return &cp
}

// Switch 切换活跃 profile
func (m *Manager) Switch(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	m.activeName = name

	// 持久化活跃标记
	activeFile := filepath.Join(m.homeDir, ".active_profile")
	if err := os.WriteFile(activeFile, []byte(name), 0o600); err != nil {
		return fmt.Errorf("write active profile: %w", err)
	}

	return nil
}

// List 列出所有 profile 名称
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.profiles))
	for name := range m.profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListWithInfo 列出所有 profile 及其信息
func (m *Manager) ListWithInfo() []ProfileInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]ProfileInfo, 0, len(m.profiles))
	for name, p := range m.profiles {
		result = append(result, ProfileInfo{
			Name:        name,
			Description: p.Description,
			Provider:    p.Provider,
			Model:       p.Model,
			Active:      name == m.activeName,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ProfileInfo profile 摘要信息
type ProfileInfo struct {
	Name        string
	Description string
	Provider    string
	Model       string
	Active      bool
}

// Delete 删除 profile
func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if name == "default" {
		return fmt.Errorf("cannot delete default profile")
	}

	if name == m.activeName {
		return fmt.Errorf("cannot delete active profile, switch first")
	}

	if _, ok := m.profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	// 删除 profile 文件
	path := filepath.Join(m.homeDir, "profiles", name+".yaml")
	_ = os.Remove(path)

	// 删除 profile 数据目录
	profileDir := m.profileDataDir(name)
	_ = os.RemoveAll(profileDir)

	delete(m.profiles, name)
	return nil
}

// Update 更新 profile 配置
func (m *Manager) Update(name string, fn func(p *Profile)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.profiles[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	fn(p)

	if err := m.saveProfile(p); err != nil {
		return err
	}

	return nil
}

// SetEnv 设置 profile 环境变量
func (m *Manager) SetEnv(name, key, value string) error {
	return m.Update(name, func(p *Profile) {
		if p.Env == nil {
			p.Env = make(map[string]string)
		}
		p.Env[key] = value
	})
}

// UnsetEnv 删除 profile 环境变量
func (m *Manager) UnsetEnv(name, key string) error {
	return m.Update(name, func(p *Profile) {
		delete(p.Env, key)
	})
}

// ApplyEnv 将 profile 的环境变量应用到当前进程
func (m *Manager) ApplyEnv(name string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.profiles[name]
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	for k, v := range p.Env {
		os.Setenv(k, v)
	}
	return nil
}

// profileDataDir 返回 profile 的数据目录
func (m *Manager) profileDataDir(name string) string {
	return filepath.Join(m.homeDir, "profiles", name+"_data")
}

// ProfileDataDir 返回 profile 的数据目录（公开）
func (m *Manager) ProfileDataDir(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profileDataDir(name)
}

// ActiveDataDir 返回当前活跃 profile 的数据目录
func (m *Manager) ActiveDataDir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profileDataDir(m.activeName)
}

// ActiveName 返回当前活跃 profile 名称
func (m *Manager) ActiveName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeName
}

// Count 返回 profile 数量
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.profiles)
}
