package feishu

import (
	"net/http"
	"strings"
)

const (
	defaultListenAddr = "127.0.0.1:6710"
	defaultPath       = "/feishu/events"
	defaultAPIBaseURL = "https://open.feishu.cn"
)

// Config holds Feishu bot callback and API settings.
type Config struct {
	AppID             string
	AppSecret         string
	VerificationToken string
	EncryptKey        string
	ListenAddr        string
	Path              string
	APIBaseURL        string
	AllowedChats      []string
	AllowedUsers      []string
	RemoveAt          bool
	GroupTriggerMode  string

	// BotOpenID can skip the startup bot-info request when the identity is
	// already known. Normally it is resolved automatically from Feishu.
	BotOpenID string

	// HTTPClient is optional and primarily useful for custom transports and
	// tests. A client with a bounded timeout is used when this is nil.
	HTTPClient *http.Client
}

// DefaultConfig returns the production defaults for the callback server.
func DefaultConfig() Config {
	return Config{
		ListenAddr:       defaultListenAddr,
		Path:             defaultPath,
		APIBaseURL:       defaultAPIBaseURL,
		RemoveAt:         true,
		GroupTriggerMode: "mention",
	}
}

func (c Config) normalizedListenAddr() string {
	if addr := strings.TrimSpace(c.ListenAddr); addr != "" {
		return addr
	}
	return defaultListenAddr
}

func (c Config) normalizedPath() string {
	path := strings.TrimSpace(c.Path)
	if path == "" {
		path = defaultPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func (c Config) normalizedAPIBaseURL() string {
	if baseURL := strings.TrimSpace(c.APIBaseURL); baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}
	return defaultAPIBaseURL
}

func (c Config) normalizedGroupTriggerMode() string {
	switch strings.ToLower(strings.TrimSpace(c.GroupTriggerMode)) {
	case "all", "open":
		return "all"
	case "none", "disabled", "off":
		return "none"
	default:
		return "mention"
	}
}

func (c Config) isChatAllowed(chatID string) bool {
	return matchesAllowlist(c.AllowedChats, chatID)
}

func (c Config) isUserAllowed(ids ...string) bool {
	if len(c.AllowedUsers) == 0 {
		return true
	}
	for _, id := range ids {
		if matchesAllowlist(c.AllowedUsers, id) {
			return true
		}
	}
	return false
}

func matchesAllowlist(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}
