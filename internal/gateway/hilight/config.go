package hilight

import "strings"

const (
	defaultWSURL                 = "wss://open.guangfan.com/open-apis/device-agent/v1/websocket"
	defaultReconnectIntervalMS   = 3000
	defaultMaxReconnectIntervalMS = 30000
	defaultHeartbeatIntervalMS   = 30000
	defaultAccountID             = "default"
	wsUUIDPlaceholder            = "{UUIDD}"
)

// Config holds HiLight WebSocket bridge settings.
type Config struct {
	Enabled                bool
	WSURL                  string
	AuthToken              string
	AccountID              string
	ReconnectIntervalMS    int
	MaxReconnectIntervalMS int
	HeartbeatIntervalMS    int
	DMPolicy               string
	AllowFrom              []string
}

func DefaultConfig() Config {
	return Config{
		Enabled:                true,
		WSURL:                  defaultWSURL,
		AccountID:              defaultAccountID,
		ReconnectIntervalMS:    defaultReconnectIntervalMS,
		MaxReconnectIntervalMS: defaultMaxReconnectIntervalMS,
		HeartbeatIntervalMS:    defaultHeartbeatIntervalMS,
		DMPolicy:               "open",
		AllowFrom:              []string{"*"},
	}
}

func (c Config) normalizedWSURL() string {
	if strings.TrimSpace(c.WSURL) == "" {
		return defaultWSURL
	}
	return strings.TrimSpace(c.WSURL)
}

func (c Config) normalizedAccountID() string {
	if strings.TrimSpace(c.AccountID) == "" {
		return defaultAccountID
	}
	return strings.TrimSpace(c.AccountID)
}

func (c Config) reconnectBase() int {
	if c.ReconnectIntervalMS <= 0 {
		return defaultReconnectIntervalMS
	}
	return c.ReconnectIntervalMS
}

func (c Config) reconnectMax() int {
	if c.MaxReconnectIntervalMS <= 0 {
		return defaultMaxReconnectIntervalMS
	}
	return c.MaxReconnectIntervalMS
}

func (c Config) heartbeatInterval() int {
	if c.HeartbeatIntervalMS <= 0 {
		return defaultHeartbeatIntervalMS
	}
	return c.HeartbeatIntervalMS
}

func (c Config) isDMAllowed(userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(c.DMPolicy)) {
	case "disabled":
		return false
	case "allowlist", "pairing":
		for _, id := range c.AllowFrom {
			id = strings.TrimSpace(id)
			if id == "*" || id == userID {
				return true
			}
		}
		return false
	default: // open
		for _, id := range c.AllowFrom {
			id = strings.TrimSpace(id)
			if id == "*" {
				return true
			}
		}
		// open with empty allowFrom still accepts, matching plugin README guidance when allowFrom=["*"]
		if len(c.AllowFrom) == 0 {
			return true
		}
		for _, id := range c.AllowFrom {
			if strings.TrimSpace(id) == userID {
				return true
			}
		}
		return false
	}
}
