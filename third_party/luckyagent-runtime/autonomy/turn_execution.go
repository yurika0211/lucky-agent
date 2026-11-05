package autonomy

import (
	"github.com/yurika0211/luckyagent-runtime/turn"
	runtime "github.com/yurika0211/luckyagent-tools/autonomy"
)

// TurnExecution adapts the autonomy queue lease to the neutral durable-turn
// contract. The adapter keeps queue persistence and stale-claim checks in this
// package while the agent loop only sees turn DTOs.
type TurnExecution struct {
	inner *runtime.Execution
}

func NewTurnExecution(inner *runtime.Execution) *TurnExecution {
	if inner == nil {
		return nil
	}
	return &TurnExecution{inner: inner}
}

func (e *TurnExecution) Snapshot() (*turn.DurableTask, error) {
	if e == nil || e.inner == nil {
		return nil, runtime.ErrStaleExecution
	}
	task, err := e.inner.Snapshot()
	if err != nil {
		return nil, err
	}
	return toTurnTask(task), nil
}

func (e *TurnExecution) BindSession(id string) error {
	if e == nil || e.inner == nil {
		return runtime.ErrStaleExecution
	}
	return e.inner.BindSession(id)
}

func (e *TurnExecution) SaveCheckpoint(data []byte) error {
	if e == nil || e.inner == nil {
		return runtime.ErrStaleExecution
	}
	return e.inner.SaveCheckpoint(data)
}

func (e *TurnExecution) BeginOperation(op turn.DurableOperation) (turn.DurableOperation, error) {
	if e == nil || e.inner == nil {
		return turn.DurableOperation{}, runtime.ErrStaleExecution
	}
	result, err := e.inner.BeginOperation(runtime.Operation{
		ID:         op.ID,
		Name:       op.Name,
		Arguments:  op.Arguments,
		State:      op.State,
		Output:     op.Output,
		Detail:     append([]byte(nil), op.Detail...),
		Failed:     op.Failed,
		ResolvedBy: op.ResolvedBy,
	})
	return turn.DurableOperation{
		ID:         result.ID,
		Name:       result.Name,
		Arguments:  result.Arguments,
		State:      result.State,
		Output:     result.Output,
		Detail:     append([]byte(nil), result.Detail...),
		Failed:     result.Failed,
		ResolvedBy: result.ResolvedBy,
	}, err
}

func (e *TurnExecution) FinishOperation(id, output string, failed bool, detail []byte) error {
	if e == nil || e.inner == nil {
		return runtime.ErrStaleExecution
	}
	return e.inner.FinishOperation(id, output, failed, append([]byte(nil), detail...))
}

func (e *TurnExecution) Finish(result *turn.DurableResult, canceled bool) error {
	if e == nil || e.inner == nil {
		return runtime.ErrStaleExecution
	}
	worker := &runtime.WorkerResult{}
	if result != nil {
		worker.Output = result.Output
		worker.Error = result.Error
		worker.Duration = result.Duration
		worker.TokensUsed = result.TokensUsed
		worker.Verified = result.Verified
		worker.Verification = result.Verification
	}
	return e.inner.Finish(worker, canceled)
}

func toTurnTask(task *runtime.QueueTask) *turn.DurableTask {
	if task == nil {
		return nil
	}
	result := &turn.DurableTask{
		SessionID:          task.SessionID,
		Checkpoint:         append([]byte(nil), task.Checkpoint...),
		AcceptanceCriteria: append([]string(nil), task.AcceptanceCriteria...),
		Verified:           task.Verified,
		Verification:       task.Verification,
		ID:                 task.ID,
		Title:              task.Title,
		Description:        task.Description,
		State:              string(task.State),
		UpdatedAt:          task.UpdatedAt,
		Operations:         make([]turn.DurableOperation, 0, len(task.Operations)),
	}
	for _, op := range task.Operations {
		result.Operations = append(result.Operations, turn.DurableOperation{
			ID: op.ID, Name: op.Name, Arguments: op.Arguments, State: op.State,
			Output: op.Output, Detail: append([]byte(nil), op.Detail...),
			Failed: op.Failed, ResolvedBy: op.ResolvedBy,
		})
	}
	return result
}
