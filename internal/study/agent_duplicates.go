package study

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var collectionBVID = regexp.MustCompile(`^BV[0-9A-Za-z]{10}$`)

// CollectionIdentity carries only candidate identifiers, never library content.
type CollectionIdentity struct {
	ExternalID string `json:"external_id"`
	URL        string `json:"url,omitempty"`
}
type CollectionDuplicate struct {
	ExternalID string `json:"external_id"`
	Exists     bool   `json:"exists"`
}

// Normalize Bilibili identities so changing BV -> URL cannot bypass a tombstone.
// No platform requests are made, and access remains confined to the workspace.
func collectionIdentity(in CollectionIdentity) (string, []string) {
	id := strings.TrimSpace(in.ExternalID)
	keys := []string{id}
	for _, raw := range []string{in.URL, id} {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		u.Fragment = ""
		keys = append(keys, u.String())
		host := strings.ToLower(u.Hostname())
		if host != "bilibili.com" && !strings.HasSuffix(host, ".bilibili.com") {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 2 && parts[0] == "video" && collectionBVID.MatchString(parts[1]) {
			id = parts[1]
		}
	}
	if collectionBVID.MatchString(id) {
		keys = append(keys, id, "https://www.bilibili.com/video/"+id, "https://www.bilibili.com/video/"+id+"/", "http://www.bilibili.com/video/"+id, "https://m.bilibili.com/video/"+id)
	}
	return id, keys
}
func collectionURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	return u.String()
}

func collectionExists(tx Tx, sources []Source, sourceID string, in CollectionIdentity) (bool, error) {
	id, keys := collectionIdentity(in)
	for _, source := range sources {
		for _, key := range keys {
			if key == "" {
				continue
			}
			if old, err := tx.FindItem(source.ID, key); err == nil {
				if source.ID == sourceID {
					return true, nil
				}
				oldID, _ := collectionIdentity(CollectionIdentity{ExternalID: old.ExternalID, URL: old.Draft.URL})
				if collectionBVID.MatchString(id) && id == oldID {
					return true, nil
				}
				// Arbitrary external IDs are only unique within a source; do not
				// suppress unrelated sites which happen to use the same local ID.
				if candidateURL := collectionURL(in.URL); candidateURL != "" && candidateURL == collectionURL(old.Draft.URL) {
					return true, nil
				}
			} else if !errors.Is(err, ErrNotFound) {
				return false, err
			}
		}
	}
	return false, nil
}

// CheckCollectionDuplicates is a task-authorized exact-match preflight. It
// returns booleans only: agents still cannot browse or read the material library.
func (s *Service) CheckCollectionDuplicates(ctx context.Context, agentID, taskID string, attempt int, inputs []CollectionIdentity) (out []CollectionDuplicate, err error) {
	if len(inputs) < 1 || len(inputs) > 20 {
		return nil, ErrInvalid
	}
	out = make([]CollectionDuplicate, 0, len(inputs))
	err = s.Store.View(ctx, func(tx Tx) error {
		task, e := recordAs[CollectionTask](tx, "agent_task", taskID)
		if e != nil {
			return e
		}
		if task.AgentID != agentID {
			return ErrTaskClaimed
		}
		if task.Attempts != attempt {
			return ErrLeaseExpired
		}
		if task.Status != TaskStatusClaimed && task.Status != TaskStatusRunning {
			return ErrConflict
		}
		if !s.leaseValid(task, s.Now()) {
			return ErrLeaseExpired
		}
		if task.CancelRequested {
			return ErrTaskCancelled
		}
		sources, e := tx.Sources()
		if e != nil {
			return e
		}
		for _, in := range inputs {
			if !validText(in.ExternalID, 2000) || !validText(in.URL, 2000) || (strings.TrimSpace(in.ExternalID) == "" && strings.TrimSpace(in.URL) == "") {
				return ErrInvalid
			}
			id, _ := collectionIdentity(in)
			exists, e := collectionExists(tx, sources, task.SourceID, in)
			if e != nil {
				return e
			}
			out = append(out, CollectionDuplicate{ExternalID: id, Exists: exists})
		}
		return nil
	})
	return
}

func filterCollectionDuplicates(tx Tx, sourceID string, inputs []Content) (out []Content, skipped int, err error) {
	sources, err := tx.Sources()
	if err != nil {
		return nil, 0, err
	}
	seen := map[string]bool{}
	out = make([]Content, 0, len(inputs))
	for _, in := range inputs {
		identity := CollectionIdentity{ExternalID: in.ExternalID, URL: in.URL}
		id, _ := collectionIdentity(identity)
		if id == "" {
			return nil, 0, ErrInvalid
		}
		exists, e := collectionExists(tx, sources, sourceID, identity)
		if e != nil {
			return nil, 0, e
		}
		if exists || seen[id] {
			skipped++
			continue
		}
		seen[id] = true
		in.ExternalID = id
		out = append(out, in)
	}
	return
}
