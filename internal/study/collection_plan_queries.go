package study

import (
	"encoding/json"
	"sort"
	"time"
)

// Optional SQL fast paths leave the Store/Tx contract unchanged for file and
// memory stores. The scoped transaction has already checked account state;
// owner predicates below must never be omitted when unwrapping workspaceTx.
func collectionQueryBase(tx Tx) (Tx, string) {
	if scoped, ok := tx.(*workspaceTx); ok {
		return scoped.Tx, scoped.owner
	}
	return tx, ""
}
func activeCollectionTasks(tx Tx, planID string) ([]CollectionTask, error) {
	base, owner := collectionQueryBase(tx)
	if sql, ok := base.(interface {
		collectionActiveTasks(string, string) ([]CollectionTask, error)
	}); ok && owner != "" {
		return sql.collectionActiveTasks(owner, planID)
	}
	all, err := recordsAs[CollectionTask](tx, "agent_task")
	if err != nil {
		return nil, err
	}
	out := []CollectionTask{}
	for _, task := range all {
		if task.PlanID == planID && (task.Status == TaskStatusPending || task.Status == TaskStatusClaimed || task.Status == TaskStatusRunning) {
			out = append(out, task)
		}
	}
	return out, nil
}

func collectionQueryRecords[T any](t *postgresTx, query string, args ...any) ([]T, error) {
	rows, err := t.tx.Query(t.ctx, query, args...)
	if err != nil {
		return nil, dbError(err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var value T
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func (t *postgresTx) collectionActiveTasks(owner, planID string) ([]CollectionTask, error) {
	return collectionQueryRecords[CollectionTask](t, `SELECT record FROM workspace_records WHERE kind='agent_task' AND COALESCE(record->>'_workspace_user_id','')=$1 AND record->>'plan_id'=$2 AND record->>'status' IN ('pending','claimed','running')`, owner, planID)
}

// Both account types are supported. The eventual scoped write rechecks the
// account and ownership under the transaction, including identity collisions.
const collectionEnabledOwnerSQL = `EXISTS (SELECT 1 FROM workspace_records AS u WHERE (u.kind='learning_user' AND u.id=p.record->>'_workspace_user_id' AND u.record->'profile'->>'enabled'='true') OR (u.kind='system_administrator' AND u.record->'profile'->>'id'=p.record->>'_workspace_user_id'))`

func (t *postgresTx) collectionDuePlans(now time.Time, limit int) ([]collectionPlanRecord, error) {
	return collectionQueryRecords[collectionPlanRecord](t, `SELECT p.record FROM workspace_records AS p WHERE p.kind='agent_collection_plan' AND p.record->>'enabled'='true' AND p.record->>'deleted_at' IS NULL AND p.record->>'next_run_at' IS NOT NULL AND (p.record->>'next_run_at')::timestamptz<=$1 AND `+collectionEnabledOwnerSQL+` ORDER BY (p.record->>'next_run_at')::timestamptz,p.id LIMIT $2`, now, limit)
}
func (t *postgresTx) collectionExpiredTasks(now time.Time, limit int) ([]CollectionTask, error) {
	return collectionQueryRecords[CollectionTask](t, `SELECT p.record FROM workspace_records AS p WHERE p.kind='agent_task' AND p.record->>'plan_id' IS NOT NULL AND ((p.record->>'status'='pending' AND (p.record->>'queue_expires_at')::timestamptz<=$1) OR (p.record->>'status' IN ('claimed','running') AND (p.record->>'lease_expires_at' IS NULL OR (p.record->>'lease_expires_at')::timestamptz<=$1))) AND `+collectionEnabledOwnerSQL+` ORDER BY p.record->>'created_at',p.id LIMIT $2`, now, limit)
}
func (t *postgresTx) collectionRunPage(owner, planID string, limit, offset int) ([]CollectionPlanRun, int, error) {
	var total int
	if err := t.tx.QueryRow(t.ctx, `SELECT count(*) FROM workspace_records WHERE kind='agent_collection_run' AND COALESCE(record->>'_workspace_user_id','')=$1 AND record->>'plan_id'=$2`, owner, planID).Scan(&total); err != nil {
		return nil, 0, dbError(err)
	}
	out, err := collectionQueryRecords[CollectionPlanRun](t, `SELECT record FROM workspace_records WHERE kind='agent_collection_run' AND COALESCE(record->>'_workspace_user_id','')=$1 AND record->>'plan_id'=$2 ORDER BY (record->>'created_at')::timestamptz DESC,id DESC LIMIT $3 OFFSET $4`, owner, planID, limit, offset)
	return out, total, err
}

func collectionRunPage(tx Tx, planID string, limit, offset int) ([]CollectionPlanRun, int, error) {
	base, owner := collectionQueryBase(tx)
	if sql, ok := base.(interface {
		collectionRunPage(string, string, int, int) ([]CollectionPlanRun, int, error)
	}); ok && owner != "" {
		return sql.collectionRunPage(owner, planID, limit, offset)
	}
	all, err := recordsAs[CollectionPlanRun](tx, collectionRunKind)
	if err != nil {
		return nil, 0, err
	}
	filtered := []CollectionPlanRun{}
	for _, run := range all {
		if run.PlanID == planID {
			filtered = append(filtered, run)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	total := len(filtered)
	if offset >= total {
		return []CollectionPlanRun{}, total, nil
	}
	return filtered[offset:min(offset+limit, total)], total, nil
}
