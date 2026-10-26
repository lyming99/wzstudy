package study

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrNotFound     = errors.New("not_found")
	ErrConflict     = errors.New("revision_conflict")
	ErrUnauthorized = errors.New("invalid_source_key")
	ErrDisabled     = errors.New("source_disabled")
	ErrForbidden    = errors.New("permission_denied")
	ErrBusy         = errors.New("source_busy")
	ErrInvalid      = errors.New("invalid_input")
)

type Source struct {
	System              bool       `json:"system,omitempty"`
	ID                  string     `json:"id"`
	Slug                string     `json:"slug"`
	Name                string     `json:"name"`
	Description         string     `json:"description"`
	Kind                string     `json:"kind"`
	Integration         string     `json:"integration"`
	MigratedClientID    string     `json:"migrated_client_id,omitempty"`
	FeedURL             string     `json:"feed_url,omitempty"`
	Category            string     `json:"category"`
	Language            string     `json:"language"`
	Enabled             bool       `json:"enabled"`
	AutoPublish         bool       `json:"auto_publish"`
	Permissions         []string   `json:"permissions"`
	PollIntervalSeconds int        `json:"poll_interval_seconds"`
	Revision            int64      `json:"revision"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	NextPollAt          time.Time  `json:"next_poll_at"`
	LastFetchedAt       *time.Time `json:"last_fetched_at,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	KeyHash             string     `json:"-"`
	ETag                string     `json:"-"`
	LastModified        string     `json:"-"`
	PollToken           string     `json:"-"`
	PollUntil           time.Time  `json:"-"`
}

type SourceInput struct {
	Slug                string   `json:"slug"`
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	Kind                string   `json:"kind"`
	Integration         string   `json:"integration"`
	FeedURL             string   `json:"feed_url"`
	Category            string   `json:"category"`
	Language            string   `json:"language"`
	Enabled             *bool    `json:"enabled,omitempty"`
	AutoPublish         bool     `json:"auto_publish"`
	Permissions         []string `json:"permissions"`
	PollIntervalSeconds int      `json:"poll_interval_seconds"`
}

type Content struct {
	Kind            string     `json:"kind,omitempty"`
	Assets          []Asset    `json:"assets,omitempty"`
	ExternalID      string     `json:"external_id"`
	Title           string     `json:"title"`
	Summary         string     `json:"summary"`
	Content         string     `json:"content"`
	Format          string     `json:"format"`
	URL             string     `json:"url,omitempty"`
	Author          string     `json:"author"`
	Category        string     `json:"category"`
	Language        string     `json:"language"`
	Tags            []string   `json:"tags"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	SourceUpdatedAt *time.Time `json:"source_updated_at,omitempty"`
}

type PublicItem struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	Revision int64  `json:"revision"`
	Content
	UpdatedAt time.Time `json:"updated_at"`
}

type Item struct {
	Status      string       `json:"status,omitempty"`
	DeletedAt   *time.Time   `json:"deleted_at,omitempty"`
	History     []AuditEvent `json:"history,omitempty"`
	ID          string       `json:"id"`
	SourceID    string       `json:"source_id"`
	ExternalID  string       `json:"external_id"`
	Revision    int64        `json:"revision"`
	Draft       Content      `json:"draft"`
	Published   *PublicItem  `json:"published,omitempty"`
	ContentHash string       `json:"content_hash"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type Change struct {
	Cursor    int64       `json:"cursor"`
	Operation string      `json:"operation"`
	ItemID    string      `json:"item_id"`
	SourceID  string      `json:"source_id"`
	Revision  int64       `json:"revision"`
	Item      *PublicItem `json:"item,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
}

type ChangePage struct {
	Changes       []Change `json:"changes"`
	NextCursor    int64    `json:"next_cursor"`
	HighWatermark int64    `json:"high_watermark"`
	HasMore       bool     `json:"has_more"`
}

type Filter struct {
	FeedbackClientID string
	FeedbackExclude  []string
	FeedbackSince    *time.Time
	Kind             string
	Status           string
	Ascending        bool
	SourceIDs        []string
	Category         string
	Tag              string
	Query            string
	Limit            int
	Offset           int
	Admin            bool
}

type IngestResult struct {
	ReceiptID  string    `json:"receipt_id"`
	SourceID   string    `json:"source_id"`
	ReceivedAt time.Time `json:"received_at"`
	Created    int       `json:"created"`
	Updated    int       `json:"updated"`
	Unchanged  int       `json:"unchanged"`
	Published  int       `json:"published"`
	Items      []Item    `json:"items"`
}

// Tx is a repeatable-read snapshot (View) or a serialized write transaction (Update).
// A write locks the global clock BEFORE reading keys or items. Cursor allocation and
// content publication commit atomically, so a reader can never skip a late commit.
type Tx interface {
	Records(string) ([]json.RawMessage, error)
	Record(string, string) (json.RawMessage, error)
	SaveRecord(string, string, json.RawMessage) error
	DeleteRecord(string, string) error
	Sources() ([]Source, error)
	Source(string) (Source, error)
	SaveSource(Source) error
	Item(string) (Item, error)
	FindItem(string, string) (Item, error)
	SaveItem(Item) error
	Items(Filter) ([]Item, error)
	Cursor() (int64, error)
	Append(Change) (Change, error)
	Changes(int64, int, []string) (ChangePage, error)
}

type Store interface {
	View(context.Context, func(Tx) error) error
	Update(context.Context, func(Tx) error) error
	Ping(context.Context) error
	Close()
}

func HashKey(key string) string { h := sha256.Sum256([]byte(key)); return hex.EncodeToString(h[:]) }
func EqualKey(hash, key string) bool {
	candidate := HashKey(key)
	return hash != "" && key != "" && subtle.ConstantTimeCompare([]byte(hash), []byte(candidate)) == 1
}
func NewKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "wzsk_" + base64.RawURLEncoding.EncodeToString(b), nil
}
func digest(c Content) string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func ValidURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Fragment == "" && len(raw) <= 2048
}
func validText(s string, n int) bool {
	return utf8.ValidString(s) && len(s) <= n && !strings.ContainsRune(s, 0)
}

func normalizeSource(in SourceInput) (SourceInput, error) {
	in.Slug = strings.TrimSpace(in.Slug)
	in.Name = strings.TrimSpace(in.Name)
	in.FeedURL = strings.TrimSpace(in.FeedURL)
	in.Category = strings.TrimSpace(in.Category)
	if in.Kind == "" {
		in.Kind = "manual"
	}
	if in.Language == "" {
		in.Language = "zh-CN"
	}
	if in.PollIntervalSeconds == 0 {
		in.PollIntervalSeconds = 900
	}
	if !slugPattern.MatchString(in.Slug) || in.Name == "" || !validText(in.Name, 200) || !validText(in.Description, 4000) || !validText(in.Category, 200) || !validText(in.Language, 35) || in.PollIntervalSeconds < 60 || in.PollIntervalSeconds > 86400 {
		return in, ErrInvalid
	}
	if in.Kind != "manual" && in.Kind != "rss" {
		return in, ErrInvalid
	}
	if in.Kind == "rss" && !ValidURL(in.FeedURL) {
		return in, ErrInvalid
	}
	if in.Kind == "manual" && in.FeedURL != "" {
		return in, ErrInvalid
	}
	if in.Kind == "rss" {
		if in.Integration != "" && in.Integration != "rss" {
			return in, ErrInvalid
		}
		in.Integration = "rss"
	} else if in.Integration != "" && in.Integration != "collector" && in.Integration != "skill" && in.Integration != "mcp" {
		return in, ErrInvalid
	}
	permissions, err := normalizePermissions(in.Permissions)
	if err != nil || (in.Kind != "manual" && permissions != nil) {
		return in, ErrInvalid
	}
	in.Permissions = permissions
	return in, nil
}

func normalizeContent(in Content, s Source) (Content, error) {
	in.ExternalID = strings.TrimSpace(in.ExternalID)
	in.Title = strings.TrimSpace(in.Title)
	if in.Format == "" {
		in.Format = "text"
	}
	if in.Category == "" {
		in.Category = s.Category
	}
	if in.Language == "" {
		in.Language = s.Language
	}
	if in.ExternalID == "" || in.Title == "" || !validText(in.ExternalID, 512) || !validText(in.Title, 500) || !validText(in.Summary, 8000) || !validText(in.Content, 262144) || !validText(in.Author, 200) || !validText(in.Category, 200) || !validText(in.Language, 35) || len(in.Tags) > 20 {
		return in, ErrInvalid
	}
	if in.Kind != "" && in.Kind != "article" && in.Kind != "audio" && in.Kind != "video" && in.Kind != "carousel" {
		return in, ErrInvalid
	}
	if len(in.Assets) > 20 {
		return in, ErrInvalid
	}
	if in.Format != "text" && in.Format != "markdown" {
		return in, ErrInvalid
	}
	if in.URL != "" && !ValidURL(in.URL) {
		return in, ErrInvalid
	}
	tags := make([]string, 0, len(in.Tags))
	seen := map[string]bool{}
	for _, tag := range in.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || !validText(tag, 80) {
			return in, ErrInvalid
		}
		if !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	sort.Strings(tags)
	in.Tags = tags
	if in.PublishedAt != nil {
		t := in.PublishedAt.UTC()
		if t.Year() < 1970 || t.Year() > 9999 {
			return in, ErrInvalid
		}
		in.PublishedAt = &t
	}
	if in.SourceUpdatedAt != nil {
		t := in.SourceUpdatedAt.UTC()
		if t.Year() < 1970 || t.Year() > 9999 {
			return in, ErrInvalid
		}
		in.SourceUpdatedAt = &t
	}
	return in, nil
}

func MatchesSource(id string, ids []string) bool {
	if len(ids) == 0 {
		return true
	}
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
func MatchesContent(c Content, f Filter) bool {
	if f.Kind != "" && Kind(c) != f.Kind {
		return false
	}
	if f.Category != "" && c.Category != f.Category {
		return false
	}
	if f.Tag != "" {
		found := false
		for _, t := range c.Tags {
			if t == f.Tag {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return f.Query == "" || strings.Contains(strings.ToLower(c.Title+" "+c.Summary+" "+c.Author+" "+c.Category+" "+strings.Join(c.Tags, " ")), strings.ToLower(f.Query))
}
