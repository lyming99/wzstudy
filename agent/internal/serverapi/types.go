package serverapi

import "time"

const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

type Constraints struct {
	MaxItems        int      `json:"max_items"`
	DuplicatePolicy string   `json:"duplicate_policy,omitempty"`
	Language        string   `json:"language,omitempty"`
	Category        string   `json:"category,omitempty"`
	ContentType     string   `json:"content_type,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	AllowDomains    []string `json:"allow_domains,omitempty"`
}

type Budget struct {
	MaxRounds      int `json:"max_rounds"`
	MaxToolCalls   int `json:"max_tool_calls"`
	TimeoutSeconds int `json:"timeout_seconds"`
}

type Task struct {
	ID              string      `json:"id"`
	SourceID        string      `json:"source_id"`
	Title           string      `json:"title"`
	Goal            string      `json:"goal"`
	Constraints     Constraints `json:"constraints"`
	Budget          Budget      `json:"budget"`
	Status          string      `json:"status"`
	Attempts        int         `json:"attempts"`
	MaxAttempts     int         `json:"max_attempts"`
	LeaseExpiresAt  *time.Time  `json:"lease_expires_at,omitempty"`
	Deadline        *time.Time  `json:"deadline,omitempty"`
	CancelRequested bool        `json:"cancel_requested"`
}

type Asset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int64  `json:"size"`
}

type Content struct {
	Kind            string     `json:"kind,omitempty"`
	ExternalID      string     `json:"external_id"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary,omitempty"`
	Content         string     `json:"content,omitempty"`
	Format          string     `json:"format,omitempty"`
	URL             string     `json:"url,omitempty"`
	Author          string     `json:"author,omitempty"`
	Assets          []Asset    `json:"assets,omitempty"`
	Category        string     `json:"category,omitempty"`
	Language        string     `json:"language,omitempty"`
	Tags            []string   `json:"tags,omitempty"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	SourceUpdatedAt *time.Time `json:"source_updated_at,omitempty"`
}

type ReceiptItem struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Status   string `json:"status"`
}

type Receipt struct {
	ID          string        `json:"id"`
	SourceID    string        `json:"source_id"`
	TaskID      string        `json:"task_id,omitempty"`
	Integration string        `json:"integration"`
	Created     int           `json:"created"`
	Updated     int           `json:"updated"`
	Unchanged   int           `json:"unchanged"`
	Skipped     int           `json:"skipped"`
	Published   int           `json:"published"`
	Items       []ReceiptItem `json:"items"`
}

type EventInput struct {
	Seq     int       `json:"seq"`
	Kind    string    `json:"kind"`
	Text    string    `json:"text,omitempty"`
	Tool    string    `json:"tool,omitempty"`
	Summary string    `json:"summary,omitempty"`
	At      time.Time `json:"at,omitempty"`
}

type Heartbeat struct {
	LeaseExpiresAt  *time.Time `json:"lease_expires_at"`
	CancelRequested bool       `json:"cancel_requested"`
	CancelReason    string     `json:"cancel_reason,omitempty"`
	Status          string     `json:"status"`
}

type Stats struct {
	ItemsCreated   int   `json:"items_created"`
	ItemsUpdated   int   `json:"items_updated"`
	ItemsUnchanged int   `json:"items_unchanged"`
	ItemsSkipped   int   `json:"items_skipped"`
	Rounds         int   `json:"rounds"`
	ToolCalls      int   `json:"tool_calls"`
	DurationMS     int64 `json:"duration_ms"`
}

type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Status struct {
	Agent struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Enabled     bool     `json:"enabled"`
		Permissions []string `json:"permissions"`
	} `json:"agent"`
	LeaseSeconds int `json:"lease_seconds"`
}
