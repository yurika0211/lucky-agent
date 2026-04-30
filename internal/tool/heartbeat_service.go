package tool

import "fmt"

// HeartbeatToolService wraps heartbeat-related tool handlers.
type HeartbeatToolService struct {
	trigger func(args map[string]any) (string, error)
	status  func(args map[string]any) (string, error)
}

// NewHeartbeatToolService creates a heartbeat tool service from injected handlers.
func NewHeartbeatToolService(
	trigger func(args map[string]any) (string, error),
	status func(args map[string]any) (string, error),
) *HeartbeatToolService {
	return &HeartbeatToolService{
		trigger: trigger,
		status:  status,
	}
}

// RegisterTools registers heartbeat-related tools onto the registry.
func (s *HeartbeatToolService) RegisterTools(r *Registry) {
	if s == nil || r == nil {
		return
	}

	r.Register(&Tool{
		Name:        "heartbeat_trigger",
		Description: "Manually trigger HEARTBEAT.md evaluation and execute any active periodic tasks.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters:  map[string]Param{},
		Handler:     s.handleTrigger,
	})
	r.Register(&Tool{
		Name:        "heartbeat_status",
		Description: "Return HEARTBEAT.md runtime status and the latest routed external chat target.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters:  map[string]Param{},
		Handler:     s.handleStatus,
	})
}

func (s *HeartbeatToolService) handleTrigger(args map[string]any) (string, error) {
	if s == nil || s.trigger == nil {
		return "", fmt.Errorf("heartbeat trigger handler not configured")
	}
	return s.trigger(args)
}

func (s *HeartbeatToolService) handleStatus(args map[string]any) (string, error) {
	if s == nil || s.status == nil {
		return "", fmt.Errorf("heartbeat status handler not configured")
	}
	return s.status(args)
}
