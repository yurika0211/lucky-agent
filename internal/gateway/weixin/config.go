package weixin

import "strings"

const defaultBaseURL = "https://ilinkai.weixin.qq.com"

type Config struct {
	Token                   string
	AccountID               string
	BaseURL                 string
	DMPolicy                string
	GroupPolicy             string
	AllowedUsers            []string
	GroupAllowedUsers       []string
	SplitMultilineMessages  bool
	PollTimeoutMilliseconds int
	SendChunkDelayMS        int
}

func DefaultConfig() Config {
	return Config{
		BaseURL:                 defaultBaseURL,
		DMPolicy:                "open",
		GroupPolicy:             "disabled",
		PollTimeoutMilliseconds: 35000,
		SendChunkDelayMS:        350,
	}
}

func (c Config) normalizedBaseURL() string {
	if strings.TrimSpace(c.BaseURL) == "" {
		return defaultBaseURL
	}
	return strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
}

func (c Config) isDMAllowed(userID string) bool {
	switch strings.ToLower(strings.TrimSpace(c.DMPolicy)) {
	case "disabled":
		return false
	case "allowlist":
		for _, id := range c.AllowedUsers {
			if strings.TrimSpace(id) == userID {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func (c Config) isGroupAllowed(chatID string) bool {
	switch strings.ToLower(strings.TrimSpace(c.GroupPolicy)) {
	case "allowlist":
		for _, id := range c.GroupAllowedUsers {
			if strings.TrimSpace(id) == chatID {
				return true
			}
		}
		return false
	case "open":
		return true
	default:
		return false
	}
}
