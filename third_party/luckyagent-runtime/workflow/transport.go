// Package workflow preserves host imports for the extracted workflow components.
package workflow

import (
	transport "github.com/yurika0211/luckyagent-api/workflow"
	core "github.com/yurika0211/luckyagent-tools/workflow"
)

type TaskStatus = core.TaskStatus
type Task = core.Task
type TaskResult = core.TaskResult
type Workflow = core.Workflow
type WorkflowInstance = core.WorkflowInstance
type Executor = core.Executor
type DefaultExecutor = core.DefaultExecutor
type ActionHandler = core.ActionHandler
type WorkflowEngine = core.WorkflowEngine

const (
	StatusPending   = core.StatusPending
	StatusRunning   = core.StatusRunning
	StatusCompleted = core.StatusCompleted
	StatusFailed    = core.StatusFailed
	StatusSkipped   = core.StatusSkipped
)

var NewWorkflow = core.NewWorkflow
var NewWorkflowInstance = core.NewWorkflowInstance
var FromJSON = core.FromJSON
var NewDefaultExecutor = core.NewDefaultExecutor
var NewWorkflowEngine = core.NewWorkflowEngine

type ServerHandlers = transport.ServerHandlers
type CreateWorkflowRequest = transport.CreateWorkflowRequest
type UpdateWorkflowRequest = transport.UpdateWorkflowRequest
type StartWorkflowRequest = transport.StartWorkflowRequest

var NewServerHandlers = transport.NewServerHandlers
