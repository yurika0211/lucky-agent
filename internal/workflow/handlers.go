package workflow

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ServerHandlers provides HTTP handlers for workflow API.
type ServerHandlers struct {
	engine *WorkflowEngine
}

// NewServerHandlers creates new workflow API handlers.
func NewServerHandlers(engine *WorkflowEngine) *ServerHandlers {
	return &ServerHandlers{engine: engine}
}

// RegisterRoutes registers workflow API routes.
func (h *ServerHandlers) RegisterRoutes(r *gin.RouterGroup) {
	workflow := r.Group("/workflows")
	{
		workflow.GET("", h.ListWorkflows)
		workflow.POST("", h.CreateWorkflow)
		workflow.GET("/:id", h.GetWorkflow)
		workflow.PUT("/:id", h.UpdateWorkflow)
		workflow.DELETE("/:id", h.DeleteWorkflow)
	}

	instances := r.Group("/workflow-instances")
	{
		instances.GET("", h.ListInstances)
		instances.POST("", h.StartWorkflow)
		instances.GET("/:id", h.GetInstance)
		instances.DELETE("/:id", h.CancelInstance)
		instances.GET("/:id/results", h.GetInstanceResults)
	}
}

// CreateWorkflowRequest is the request body for creating a workflow.
type CreateWorkflowRequest struct {
	Name        string  `json:"name" binding:"required"`
	Description string  `json:"description,omitempty"`
	Tasks       []*Task `json:"tasks" binding:"required"`
	Version     string  `json:"version,omitempty"`
}

// UpdateWorkflowRequest is the request body for updating a workflow.
type UpdateWorkflowRequest struct {
	Name        string  `json:"name,omitempty"`
	Description string  `json:"description,omitempty"`
	Tasks       []*Task `json:"tasks,omitempty"`
	Version     string  `json:"version,omitempty"`
}

// StartWorkflowRequest is the request body for starting a workflow.
type StartWorkflowRequest struct {
	WorkflowID string `json:"workflowId" binding:"required"`
}

// ListWorkflows handles GET /workflows.
func (h *ServerHandlers) ListWorkflows(c *gin.Context) {
	workflows := h.engine.ListWorkflows()
	c.JSON(http.StatusOK, gin.H{
		"workflows": workflows,
		"count":     len(workflows),
	})
}

// CreateWorkflow handles POST /workflows.
func (h *ServerHandlers) CreateWorkflow(c *gin.Context) {
	var req CreateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	workflow := &Workflow{
		ID:          generateID(),
		Name:        req.Name,
		Description: req.Description,
		Tasks:       req.Tasks,
		Version:     req.Version,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := h.engine.RegisterWorkflow(workflow); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, workflow)
}

// GetWorkflow handles GET /workflows/:id.
func (h *ServerHandlers) GetWorkflow(c *gin.Context) {
	id := c.Param("id")
	workflow, ok := h.engine.GetWorkflow(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		return
	}
	c.JSON(http.StatusOK, workflow)
}

// UpdateWorkflow handles PUT /workflows/:id.
func (h *ServerHandlers) UpdateWorkflow(c *gin.Context) {
	id := c.Param("id")
	workflow, ok := h.engine.GetWorkflow(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		return
	}

	var req UpdateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != "" {
		workflow.Name = req.Name
	}
	if req.Description != "" {
		workflow.Description = req.Description
	}
	if req.Tasks != nil {
		workflow.Tasks = req.Tasks
	}
	if req.Version != "" {
		workflow.Version = req.Version
	}
	workflow.UpdatedAt = time.Now()

	if err := workflow.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, workflow)
}

// DeleteWorkflow handles DELETE /workflows/:id.
func (h *ServerHandlers) DeleteWorkflow(c *gin.Context) {
	id := c.Param("id")
	if err := h.engine.DeleteWorkflow(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "workflow deleted"})
}

// ListInstances handles GET /workflow-instances.
func (h *ServerHandlers) ListInstances(c *gin.Context) {
	instances := h.engine.ListInstances()
	c.JSON(http.StatusOK, gin.H{
		"instances": instances,
		"count":     len(instances),
	})
}

// StartWorkflow handles POST /workflow-instances.
func (h *ServerHandlers) StartWorkflow(c *gin.Context) {
	var req StartWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	instance, err := h.engine.StartWorkflow(req.WorkflowID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Return a snapshot to avoid race condition with the running goroutine
	c.JSON(http.StatusCreated, instance.Snapshot())
}

// GetInstance handles GET /workflow-instances/:id.
func (h *ServerHandlers) GetInstance(c *gin.Context) {
	id := c.Param("id")
	instance, ok := h.engine.GetInstance(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}
	c.JSON(http.StatusOK, instance.Snapshot())
}

// CancelInstance handles DELETE /workflow-instances/:id.
func (h *ServerHandlers) CancelInstance(c *gin.Context) {
	id := c.Param("id")
	if err := h.engine.CancelInstance(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "instance cancelled"})
}

// GetInstanceResults handles GET /workflow-instances/:id/results.
func (h *ServerHandlers) GetInstanceResults(c *gin.Context) {
	id := c.Param("id")
	instance, ok := h.engine.GetInstance(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "instance not found"})
		return
	}

	snapshot := instance.Snapshot()
	c.JSON(http.StatusOK, gin.H{
		"instanceId": snapshot.ID,
		"status":     snapshot.Status,
		"results":    snapshot.Results,
	})
}

func generateID() string {
	return time.Now().Format("20060102") + "-" + randomString(8)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
}
