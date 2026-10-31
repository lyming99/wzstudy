package serverapi

import "context"

type CollectionIdentity struct {
	ExternalID string `json:"external_id"`
	URL        string `json:"url,omitempty"`
}
type CollectionDuplicate struct {
	ExternalID string `json:"external_id"`
	Exists     bool   `json:"exists"`
}

func (c *Client) CheckDuplicates(ctx context.Context, taskID string, attempt int, items []CollectionIdentity) ([]CollectionDuplicate, error) {
	var out struct {
		Items []CollectionDuplicate `json:"items"`
	}
	_, err := c.do(ctx, "POST", "/agent/tasks/"+taskID+"/duplicates", map[string]any{"attempt": attempt, "items": items}, &out)
	return out.Items, err
}
