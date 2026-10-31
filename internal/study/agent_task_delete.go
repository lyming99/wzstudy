package study

import "context"

// DeleteCollectionTask removes a finished task and its progress events. Running
// tasks must be cancelled first so an agent cannot keep writing to a deleted
// task.
func (s *Service) DeleteCollectionTask(ctx context.Context, id string) (out CollectionTask, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		task, e := recordAs[CollectionTask](tx, "agent_task", id)
		if e != nil {
			return e
		}
		switch task.Status {
		case TaskStatusSucceeded, TaskStatusFailed, TaskStatusCancelled, TaskStatusExpired:
		default:
			return ErrBusy
		}
		events, e := recordsAs[AgentEvent](tx, "agent_event")
		if e != nil {
			return e
		}
		for _, event := range events {
			if event.TaskID != id {
				continue
			}
			if e := tx.DeleteRecord("agent_event", agentEventID(id, event.Attempt, event.Seq)); e != nil {
				return e
			}
		}
		out = task
		return tx.DeleteRecord("agent_task", id)
	})
	return
}
