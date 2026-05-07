package qqofficial

import "strings"

// Config holds QQ official bot gateway configuration.
type Config struct {
	AppID         string
	AppSecret     string
	Sandbox       bool
	APIBaseURL    string
	GatewayURL    string
	AllowedChats  []string
	AllowedUsers  []string
	RemoveAt      bool
	HeartbeatSec  int
	ReconnectWait int
	Intents       []string
}

func DefaultConfig() Config {
	return Config{
		RemoveAt:      true,
		HeartbeatSec:  25,
		ReconnectWait: 5,
		Intents: []string{
			"public_guild_messages",
			"group_and_c2c_messages",
		},
	}
}

func (c Config) normalizedAPIBaseURL() string {
	if strings.TrimSpace(c.APIBaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(c.APIBaseURL), "/")
	}
	if c.Sandbox {
		return "https://sandbox.api.sgroup.qq.com"
	}
	return "https://api.sgroup.qq.com"
}

func (c Config) normalizedGatewayURL() string {
	if strings.TrimSpace(c.GatewayURL) != "" {
		return strings.TrimSpace(c.GatewayURL)
	}
	if c.Sandbox {
		return "wss://sandbox.api.sgroup.qq.com/websocket"
	}
	return "wss://api.sgroup.qq.com/websocket"
}

func (c Config) IsChatAllowed(chatID string) bool {
	if len(c.AllowedChats) == 0 {
		return true
	}
	for _, id := range c.AllowedChats {
		if strings.TrimSpace(id) == chatID {
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
