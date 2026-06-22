package telegram

// Config holds Telegram-specific configuration.
type Config struct {
	Token                     string   // Bot token
	Proxy                     string   // Optional proxy URL for Telegram API (http/https/socks5)
	AllowedChats              []string // Chat ID whitelist (empty = allow all)
	AdminIDs                  []string // Admin user IDs
	MaxMessageLen             int      // Max message length before splitting (default 4000)
	RateLimit                 int      // Messages per second per chat (default 1)
	PollTimeout               int      // Long polling timeout in seconds (default 30)
	AttachmentDownloadLimit   int64    // Max inbound attachment bytes to cache locally (default 1 GiB)
	AttachmentDownloadTimeout int      // Attachment download timeout in seconds (default 30)
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxMessageLen:             4000,
		RateLimit:                 1,
		PollTimeout:               30,
		AttachmentDownloadLimit:   defaultAttachmentDownloadLimit,
		AttachmentDownloadTimeout: 30,
	}
}

// IsChatAllowed returns true if the chat ID is in the whitelist, or if the whitelist is empty.
func (c Config) IsChatAllowed(chatID string) bool {
	if len(c.AllowedChats) == 0 {
		return true
	}
	for _, id := range c.AllowedChats {
		if id == chatID {
			return true
		}
	}
	return false
}

// IsAdmin returns true if the user ID is in the admin list.
func (c Config) IsAdmin(userID string) bool {
	for _, id := range c.AdminIDs {
		if id == userID {
			return true
		}
	}
	return false
}
