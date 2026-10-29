package agent

import (
	"encoding/json"

	"github.com/yurika0211/luckyagent-runtime/openui"
)

// EmitUIDelta validates a catalog tree and returns a ChatEvent. Unknown
// components are dropped. An empty remaining tree yields a nil event so the
// caller keeps the Markdown path.
func EmitUIDelta(raw json.RawMessage) (*ChatEvent, []string, error) {
	delta, dropped, err := openui.ValidateDelta(raw)
	if err != nil {
		return nil, dropped, err
	}
	if len(delta.Nodes) == 0 {
		return nil, dropped, nil
	}
	return &ChatEvent{Type: ChatEventUIDelta, UI: &delta}, dropped, nil
}
