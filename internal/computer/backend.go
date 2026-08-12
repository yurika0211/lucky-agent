package computer

import "context"

// Capabilities describes operations supported by a platform backend.
type Capabilities struct {
	Capture       bool
	Click         bool
	DoubleClick   bool
	Move          bool
	Drag          bool
	TypeText      bool
	Keypress      bool
	Scroll        bool
	MultiDisplay  bool
	ScaleFactor   float64
	BackendDetail string
}

// Backend isolates operating-system-specific screenshot and input operations.
type Backend interface {
	Name() string
	Capabilities(ctx context.Context) (Capabilities, error)
	Capture(ctx context.Context, target Target) (Observation, error)
	Perform(ctx context.Context, action Action) error
	Close() error
}
