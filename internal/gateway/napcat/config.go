package napcat

import "strings"

// Config holds NapCat / OneBot v11 reverse WebSocket gateway settings.
type Config struct {
	ListenAddr       string
	Path             string
	AccessToken      string
	AllowedChats     []string
	AllowedUsers     []string
	RemoveAt         bool
	GroupTriggerMode string
}

func DefaultConfig() Config {
	return Config{
		ListenAddr:       "127.0.0.1:6701",
		Path:             "/onebot/v11/ws",
		RemoveAt:         true,
		GroupTriggerMode: "mention",
	}
}

func (c Config) normalizedPath() string {
	path := strings.TrimSpace(c.Path)
	if path == "" {
		path = DefaultConfig().Path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func (c Config) normalizedListenAddr() string {
	if strings.TrimSpace(c.ListenAddr) != "" {
		return strings.TrimSpace(c.ListenAddr)
	}
	return DefaultConfig().ListenAddr
}

func (c Config) normalizedGroupTriggerMode() string {
	mode := strings.ToLower(strings.TrimSpace(c.GroupTriggerMode))
	switch mode {
	case "all", "open":
		return "all"
	case "none", "disabled", "off":
		return "none"
	default:
		return "mention"
	}
}

func (c Config) IsChatAllowed(chatID string, rawID string) bool {
	if len(c.AllowedChats) == 0 {
		return true
	}
	for _, id := range c.AllowedChats {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if id == chatID || id == rawID {
			return true
		}
	}
	return false
}

func (c Config) IsUserAllowed(userID string) bool {
	if len(c.AllowedUsers) == 0 {
		return true
	}
	for _, id := range c.AllowedUsers {
		if strings.TrimSpace(id) == userID {
			return true
		}
	}
	return false
}
