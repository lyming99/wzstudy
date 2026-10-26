package study

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Asset is immutable. IDs, names, MIME types and sizes are assigned by the upload
// handler; incoming asset objects are resolved against the registry before use.
type Asset struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Size      int64     `json:"size"`
	SourceID  string    `json:"source_id"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url"`
}
type AuditEvent struct {
	Action string    `json:"action"`
	At     time.Time `json:"at"`
}
type WorkspaceSettings struct {
	Name     string `json:"name"`
	Compact  bool   `json:"compact"`
	Revision int64  `json:"revision"`
}
type ManagedInput struct {
	Revision int64   `json:"revision"`
	SourceID string  `json:"source_id"`
	Status   string  `json:"status"`
	Content  Content `json:"content"`
}
type DayCount struct {
	Date     string `json:"date"`
	Client   int    `json:"client"`
	Internet int    `json:"internet"`
}
type Summary struct {
	Total      int            `json:"total"`
	Pending    int            `json:"pending"`
	Draft      int            `json:"draft"`
	Published  int            `json:"published"`
	WeekNew    int            `json:"week_new"`
	ByKind     map[string]int `json:"by_kind"`
	ByCategory map[string]int `json:"by_category"`
	BySource   map[string]int `json:"by_source"`
	Trends     []DayCount     `json:"trends"`
	AssetBytes int64          `json:"asset_bytes"`
	AssetCount int            `json:"asset_count"`
}
type Workspace struct {
	Sources   []Source                 `json:"sources"`
	Summary   Summary                  `json:"summary"`
	Clients   []Client                 `json:"clients"`
	Migration IdentityMigrationPreview `json:"identity_migration"`
	Settings  WorkspaceSettings        `json:"settings"`
	Recent    []Item                   `json:"recent"`
}

func recordSave(tx Tx, kind, id string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return tx.SaveRecord(kind, id, b)
}
func recordsAs[T any](tx Tx, kind string) ([]T, error) {
	raw, e := tx.Records(kind)
	if e != nil {
		return nil, e
	}
	out := make([]T, 0, len(raw))
	for _, b := range raw {
		var v T
		if e = json.Unmarshal(b, &v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func recordAs[T any](tx Tx, kind, id string) (v T, e error) {
	b, e := tx.Record(kind, id)
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(b, &v)
	return
}
func Kind(c Content) string {
	if c.Kind == "" {
		return "article"
	}
	return c.Kind
}
func ReviewState(v Item) string {
	if v.Status != "" {
		return v.Status
	}
	if v.Published != nil && v.Published.Revision == v.Revision {
		return "published"
	}
	return "pending"
}
func audit(v *Item, action string, at time.Time) {
	v.History = append(v.History, AuditEvent{Action: action, At: at})
	if len(v.History) > 50 {
		v.History = append([]AuditEvent(nil), v.History[len(v.History)-50:]...)
	}
}
func allItems(tx Tx) ([]Item, error) { return tx.Items(Filter{Admin: true, Limit: 2147483647}) }
func studioSource(tx Tx, now time.Time) (Source, error) {
	sources, e := tx.Sources()
	if e != nil {
		return Source{}, e
	}
	for _, v := range sources {
		if v.System {
			return v, nil
		}
	}
	v := Source{ID: uuid.NewString(), Slug: "studio-workspace-" + uuid.NewString(), Name: "管理工作台", Description: "管理员上传与互联网链接入库", Kind: "manual", System: true, Enabled: true, Language: "zh-CN", PollIntervalSeconds: 900, Revision: 1, CreatedAt: now, UpdatedAt: now, NextPollAt: now}
	return v, tx.SaveSource(v)
}
func normalizeAssets(tx Tx, c *Content, sourceID string, admin bool) error {
	for n, a := range c.Assets {
		v, e := recordAs[Asset](tx, "asset", a.ID)
		if e != nil {
			return ErrInvalid
		}
		if !admin && v.SourceID != sourceID {
			return ErrUnauthorized
		}
		c.Assets[n] = v
	}
	if Kind(*c) == "carousel" && len(c.Assets) > 20 {
		return ErrInvalid
	}
	for _, a := range c.Assets {
		k := Kind(*c)
		if (k == "audio" && !strings.HasPrefix(a.MIME, "audio/")) || (k == "video" && !strings.HasPrefix(a.MIME, "video/")) || (k == "carousel" && !strings.HasPrefix(a.MIME, "image/")) || (k == "article" && a.MIME != "text/plain" && a.MIME != "text/markdown") {
			return ErrInvalid
		}
	}
	if (Kind(*c) == "audio" || Kind(*c) == "video") && len(c.Assets) > 1 {
		return ErrInvalid
	}
	if Kind(*c) != "article" && len(c.Assets) == 0 && c.URL == "" {
		return ErrInvalid
	}
	return nil
}
func registerAssets(tx Tx, assets []Asset, sourceID string) error {
	for _, a := range assets {
		if _, e := uuid.Parse(a.ID); e != nil || a.Size < 1 || a.Name == "" {
			return ErrInvalid
		}
		if _, e := tx.Record("asset", a.ID); e == nil {
			return ErrConflict
		} else if e != ErrNotFound {
			return e
		}
		a.SourceID = sourceID
		a.URL = "/api/v1/media/" + a.ID
		if e := recordSave(tx, "asset", a.ID, a); e != nil {
			return e
		}
	}
	return nil
}

func (s *Service) SaveManaged(ctx context.Context, id string, in ManagedInput, assets []Asset) (out Item, err error) {
	if in.Status != "draft" && in.Status != "pending" {
		return out, ErrInvalid
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		now := s.Now()
		var v Item
		var source Source
		var e error
		if id == "" {
			if in.Revision != 0 {
				return ErrInvalid
			}
			if in.SourceID == "" {
				source, e = studioSource(tx, now)
			} else {
				source, e = tx.Source(in.SourceID)
			}
			if e != nil {
				return e
			}
			v = Item{ID: uuid.NewString(), SourceID: source.ID, ExternalID: uuid.NewString(), CreatedAt: now}
			in.Content.ExternalID = v.ExternalID
		} else {
			v, e = tx.Item(id)
			if e != nil || v.DeletedAt != nil {
				return ErrNotFound
			}
			if v.Revision != in.Revision {
				return ErrConflict
			}
			if in.SourceID != "" && in.SourceID != v.SourceID {
				return ErrInvalid
			}
			source, e = tx.Source(v.SourceID)
			if e != nil {
				return e
			}
			in.Content.ExternalID = v.ExternalID
		}
		if e = registerAssets(tx, assets, source.ID); e != nil {
			return e
		}
		c, e := normalizeContent(in.Content, source)
		if e != nil {
			return e
		}
		if e = normalizeAssets(tx, &c, source.ID, true); e != nil {
			return e
		}
		v.Draft = c
		v.ContentHash = digest(c)
		v.Status = in.Status
		v.Revision++
		v.UpdatedAt = now
		audit(&v, "资料已保存为"+map[string]string{"draft": "草稿", "pending": "待审核"}[v.Status], now)
		if e = tx.SaveItem(v); e != nil {
			return e
		}
		result := IngestResult{Items: []Item{v}}
		if id == "" {
			result.Created = 1
		} else {
			result.Updated = 1
		}
		if _, e = s.recordIntake(tx, source, result, "manual"); e != nil {
			return e
		}
		out = v
		return nil
	})
	return
}
func (s *Service) IngestAssets(ctx context.Context, id, key string, c Content, assets []Asset) (out IngestResult, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		source, e := sourceAccess(tx, id, key, PermissionIngest, PermissionUpload)
		if e != nil {
			return e
		}
		if e = registerAssets(tx, assets, id); e != nil {
			return e
		}
		out, e = s.ingest(tx, source, []Content{c})
		return e
	})
	return
}
func (s *Service) Submit(ctx context.Context, id string, rev int64) (out Item, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		v, e := tx.Item(id)
		if e != nil || v.DeletedAt != nil {
			return ErrNotFound
		}
		if v.Revision != rev {
			return ErrConflict
		}
		if ReviewState(v) != "draft" {
			return ErrInvalid
		}
		v.Status = "pending"
		v.Revision++
		v.UpdatedAt = s.Now()
		audit(&v, "草稿已提交审核", s.Now())
		out = v
		return tx.SaveItem(v)
	})
	return
}
func (s *Service) DeleteItem(ctx context.Context, id string, rev int64) error {
	return s.Store.Update(ctx, func(tx Tx) error {
		v, e := tx.Item(id)
		if e != nil || v.DeletedAt != nil {
			return ErrNotFound
		}
		if v.Revision != rev {
			return ErrConflict
		}
		v.Revision++
		now := s.Now()
		v.UpdatedAt = now
		if v.Published != nil {
			if e = s.publish(tx, &v, false); e != nil {
				return e
			}
		}
		v.DeletedAt = &now
		audit(&v, "资料已删除", now)
		return tx.SaveItem(v)
	})
}
func (s *Service) Asset(ctx context.Context, id string, admin bool) (out Asset, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		var e error
		out, e = recordAs[Asset](tx, "asset", id)
		if e != nil {
			return e
		}
		items, e := allItems(tx)
		if e != nil {
			return e
		}
		for _, v := range items {
			var c Content
			if admin {
				c = v.Draft
			} else if v.Published != nil {
				c = v.Published.Content
			} else {
				continue
			}
			for _, a := range c.Assets {
				if a.ID == id {
					return nil
				}
			}
			if admin && v.Published != nil {
				for _, a := range v.Published.Assets {
					if a.ID == id {
						return nil
					}
				}
			}
		}
		return ErrNotFound
	})
	return
}
func (s *Service) AdminPage(ctx context.Context, f Filter) (out []Item, total int, err error) {
	if f.Limit < 1 || f.Limit > 100 || f.Offset < 0 || f.Offset > 10000 || len(f.Query) > 200 || len(f.SourceIDs) > 20 {
		return nil, 0, ErrInvalid
	}
	f.Admin = true
	size, offset := f.Limit, f.Offset
	f.Limit = 2147483647
	f.Offset = 0
	err = s.Store.View(ctx, func(tx Tx) error {
		all, e := tx.Items(f)
		if e != nil {
			return e
		}
		total = len(all)
		if offset >= total {
			out = []Item{}
			return nil
		}
		out = all[offset:min(total, offset+size)]
		for n := range out {
			out[n].Draft.Content = ""
			if out[n].Published != nil {
				out[n].Published.Content.Content = ""
			}
		}
		return nil
	})
	return
}
func (s *Service) Workspace(ctx context.Context) (out Workspace, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		var e error
		out.Sources, e = tx.Sources()
		if e != nil {
			return e
		}
		for i := range out.Sources {
			out.Sources[i] = sourcePolicy(out.Sources[i])
		}
		clients, e := recordsAs[clientRecord](tx, "client")
		if e != nil {
			return e
		}
		out.Clients = []Client{}
		for _, record := range clients {
			out.Clients = append(out.Clients, cloneClient(record.Client))
		}
		sort.Slice(out.Clients, func(i, j int) bool { return out.Clients[i].Slug < out.Clients[j].Slug })
		out.Migration, e = identityPreview(tx)
		if e != nil {
			return e
		}
		out.Settings, e = recordAs[WorkspaceSettings](tx, "settings", "workspace")
		if e == ErrNotFound {
			out.Settings = WorkspaceSettings{Name: "管理员"}
			e = nil
		}
		if e != nil {
			return e
		}
		items, e := allItems(tx)
		if e != nil {
			return e
		}
		stats := Summary{ByKind: map[string]int{}, ByCategory: map[string]int{}, BySource: map[string]int{}, Trends: []DayCount{}}
		days := map[string]*DayCount{}
		start := s.Now().UTC().Truncate(24 * time.Hour).Add(-6 * 24 * time.Hour)
		for i := 0; i < 7; i++ {
			d := DayCount{Date: start.Add(time.Duration(i) * 24 * time.Hour).Format("2006-01-02")}
			stats.Trends = append(stats.Trends, d)
		}
		for i := range stats.Trends {
			days[stats.Trends[i].Date] = &stats.Trends[i]
		}
		sourceKinds := map[string]string{}
		for _, source := range out.Sources {
			sourceKinds[source.ID] = source.Kind
		}
		for _, v := range items {
			stats.Total++
			switch ReviewState(v) {
			case "published":
				stats.Published++
			case "draft":
				stats.Draft++
			default:
				stats.Pending++
			}
			stats.ByKind[Kind(v.Draft)]++
			stats.ByCategory[v.Draft.Category]++
			stats.BySource[v.SourceID]++
			if !v.CreatedAt.Before(start) {
				stats.WeekNew++
			}
			if d := days[v.CreatedAt.UTC().Format("2006-01-02")]; d != nil {
				if sourceKinds[v.SourceID] == "rss" || v.Draft.URL != "" {
					d.Internet++
				} else {
					d.Client++
				}
			}
		}
		assets, e := recordsAs[Asset](tx, "asset")
		if e != nil {
			return e
		}
		stats.AssetCount = len(assets)
		for _, a := range assets {
			stats.AssetBytes += a.Size
		}
		out.Summary = stats
		out.Recent = append([]Item{}, items[:min(6, len(items))]...)
		for i := range out.Recent {
			out.Recent[i].Draft.Content = ""
			out.Recent[i].Published = nil
		}
		return nil
	})
	return
}
func (s *Service) SaveSettings(ctx context.Context, in WorkspaceSettings) (out WorkspaceSettings, err error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validText(in.Name, 80) {
		return out, ErrInvalid
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		old, e := recordAs[WorkspaceSettings](tx, "settings", "workspace")
		if e != nil && e != ErrNotFound {
			return e
		}
		if old.Revision != in.Revision {
			return ErrConflict
		}
		in.Revision++
		out = in
		return recordSave(tx, "settings", "workspace", in)
	})
	return
}
