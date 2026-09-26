package sdk

import "time"

// SessionInfo is lightweight session metadata for listings.
type SessionInfo struct {
	ID           string
	Title        string
	MessageCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Message is a simplified chat transcript entry.
type Message struct {
	Role    string
	Content string
	Name    string
}

// Session is a full session snapshot for host UIs.
type Session struct {
	ID           string
	Title        string
	MessageCount int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Messages     []Message
}
