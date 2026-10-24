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
type Collection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	Color       string    `json:"color"`
	Revision    int64     `json:"revision"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type PushPlan struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	ItemIDs     []string   `json:"item_ids"`
	TargetIDs   []string   `json:"target_ids"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	Status      string     `json:"status"`
	Revision    int64      `json:"revision"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	Deliveries  int        `json:"deliveries"`
}
type Delivery struct {
	Cursor    int64      `json:"cursor"`
	PlanID    string     `json:"plan_id"`
	PlanTitle string     `json:"plan_title"`
	TargetIDs []string   `json:"target_ids"`
	Item      PublicItem `json:"item"`
	CreatedAt time.Time  `json:"created_at"`
}
type InboxPage struct {
	Deliveries    []Delivery `json:"deliveries"`
	NextCursor    int64      `json:"next_cursor"`
	HighWatermark int64      `json:"high_watermark"`
	HasMore       bool       `json:"has_more"`
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
	Sources     []Source          `json:"sources"`
	Summary     Summary           `json:"summary"`
	Collections []Collection      `json:"collections"`
	Plans       []PushPlan        `json:"plans"`
	Settings    WorkspaceSettings `json:"settings"`
	Recent      []Item            `json:"recent"`
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
		out = v
		return nil
	})
	return
}
func (s *Service) IngestAssets(ctx context.Context, id, key string, c Content, assets []Asset) (out IngestResult, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		source, e := tx.Source(id)
		if e != nil || source.System || !EqualKey(source.KeyHash, key) {
			return ErrUnauthorized
		}
		if !source.Enabled {
			return ErrDisabled
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
		out.Collections, e = recordsAs[Collection](tx, "collection")
		if e != nil {
			return e
		}
		out.Plans, e = recordsAs[PushPlan](tx, "plan")
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
		sort.Slice(out.Plans, func(i, j int) bool { return out.Plans[i].ScheduledAt.Before(out.Plans[j].ScheduledAt) })
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
func (s *Service) SaveCollection(ctx context.Context, id string, in Collection) (out Collection, err error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Category = strings.TrimSpace(in.Category)
	if in.Name == "" || in.Category == "" || !validText(in.Name, 150) || !validText(in.Category, 200) || !validText(in.Description, 1000) || (in.Color != "green" && in.Color != "purple" && in.Color != "blue") {
		return out, ErrInvalid
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		if id == "" {
			if in.Revision != 0 {
				return ErrInvalid
			}
			id = uuid.NewString()
		} else {
			old, e := recordAs[Collection](tx, "collection", id)
			if e != nil {
				return e
			}
			if old.Revision != in.Revision {
				return ErrConflict
			}
		}
		in.ID = id
		in.Revision++
		in.UpdatedAt = s.Now()
		out = in
		return recordSave(tx, "collection", id, in)
	})
	return
}
func (s *Service) DeleteCollection(ctx context.Context, id string, rev int64) error {
	return s.Store.Update(ctx, func(tx Tx) error {
		v, e := recordAs[Collection](tx, "collection", id)
		if e != nil {
			return e
		}
		if v.Revision != rev {
			return ErrConflict
		}
		return tx.DeleteRecord("collection", id)
	})
}
func validateTargets(tx Tx, ids []string) error {
	seen := map[string]bool{}
	if len(ids) > 100 {
		return ErrInvalid
	}
	for _, id := range ids {
		v, e := tx.Source(id)
		if e != nil || v.Kind != "manual" || v.System || !v.Enabled || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	return nil
}
func (s *Service) CreatePlan(ctx context.Context, in PushPlan) (out PushPlan, err error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || !validText(in.Title, 150) || len(in.ItemIDs) < 1 || len(in.ItemIDs) > 50 || !in.ScheduledAt.After(s.Now()) || in.ScheduledAt.After(s.Now().AddDate(2, 0, 0)) {
		return out, ErrInvalid
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		if e := validateTargets(tx, in.TargetIDs); e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, id := range in.ItemIDs {
			v, e := tx.Item(id)
			if e != nil || v.DeletedAt != nil || v.Published == nil || seen[id] {
				return ErrInvalid
			}
			seen[id] = true
		}
		out = PushPlan{ID: uuid.NewString(), Title: in.Title, ItemIDs: append([]string(nil), in.ItemIDs...), TargetIDs: append([]string{}, in.TargetIDs...), ScheduledAt: in.ScheduledAt.UTC(), Status: "scheduled", Revision: 1, CreatedAt: s.Now()}
		return recordSave(tx, "plan", out.ID, out)
	})
	return
}
func (s *Service) PlanAction(ctx context.Context, id string, rev int64, action string) (out PushPlan, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		v, e := recordAs[PushPlan](tx, "plan", id)
		if e != nil {
			return e
		}
		if v.Revision != rev {
			return ErrConflict
		}
		switch action {
		case "delete":
			return tx.DeleteRecord("plan", id)
		case "pause":
			if v.Status != "scheduled" {
				return ErrInvalid
			}
			v.Status = "paused"
		case "resume":
			if v.Status != "paused" && v.Status != "failed" {
				return ErrInvalid
			}
			v.Status = "scheduled"
			v.LastError = ""
		case "run":
			if v.Status != "scheduled" && v.Status != "paused" && v.Status != "failed" {
				return ErrInvalid
			}
			if e = s.deliver(tx, &v); e != nil {
				return e
			}
		default:
			return ErrInvalid
		}
		v.Revision++
		out = v
		return recordSave(tx, "plan", id, v)
	})
	return
}

// Each plan, its outbox events and inbox deliveries commit in the same store
// transaction. The feed clock serializes writers across PostgreSQL processes.
func (s *Service) deliver(tx Tx, p *PushPlan) error {
	targets := append([]string{}, p.TargetIDs...)
	if len(targets) == 0 {
		sources, e := tx.Sources()
		if e != nil {
			return e
		}
		for _, v := range sources {
			if v.Kind == "manual" && !v.System && v.Enabled {
				targets = append(targets, v.ID)
			}
		}
	}
	if len(targets) == 0 {
		p.Status = "failed"
		p.LastError = "没有可接收内容的已启用客户端，请先接入客户端"
		return nil
	}
	if e := validateTargets(tx, targets); e != nil {
		p.Status = "failed"
		p.LastError = "接收客户端已停用或不存在"
		return nil
	}
	items := make([]PublicItem, 0, len(p.ItemIDs))
	for _, id := range p.ItemIDs {
		v, e := tx.Item(id)
		if e != nil && e != ErrNotFound {
			return e
		}
		if e == ErrNotFound || v.DeletedAt != nil || v.Published == nil {
			p.Status = "failed"
			p.LastError = "计划中的资料已下架或删除，请重新创建计划"
			return nil
		}
		items = append(items, *v.Published)
	}
	now := s.Now()
	for _, item := range items {
		event, e := tx.Append(Change{Operation: "upsert", ItemID: item.ID, SourceID: item.SourceID, Revision: item.Revision, Item: &item, CreatedAt: now})
		if e != nil {
			return e
		}
		d := Delivery{Cursor: event.Cursor, PlanID: p.ID, PlanTitle: p.Title, TargetIDs: targets, Item: item, CreatedAt: now}
		if e = recordSave(tx, "delivery", uuid.NewString(), d); e != nil {
			return e
		}
	}
	p.Status = "done"
	p.CompletedAt = &now
	p.LastError = ""
	p.Deliveries = len(items) * len(targets)
	return nil
}
func (s *Service) DispatchDue(ctx context.Context) error {
	// Avoid rewriting a durable file snapshot every two seconds when nothing is due.
	due := false
	if err := s.Store.View(ctx, func(tx Tx) error {
		plans, e := recordsAs[PushPlan](tx, "plan")
		if e != nil {
			return e
		}
		for _, p := range plans {
			if p.Status == "scheduled" && !p.ScheduledAt.After(s.Now()) {
				due = true
				break
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if !due {
		return nil
	}
	return s.Store.Update(ctx, func(tx Tx) error {
		plans, e := recordsAs[PushPlan](tx, "plan")
		if e != nil {
			return e
		}
		sort.Slice(plans, func(i, j int) bool { return plans[i].ScheduledAt.Before(plans[j].ScheduledAt) })
		processed := 0
		for _, v := range plans {
			if v.Status != "scheduled" || v.ScheduledAt.After(s.Now()) {
				continue
			}
			if e = s.deliver(tx, &v); e != nil {
				return e
			}
			v.Revision++
			if e = recordSave(tx, "plan", v.ID, v); e != nil {
				return e
			}
			processed++
			if processed >= 20 {
				break
			}
		}
		return nil
	})
}
func (s *Service) Inbox(ctx context.Context, id, key string, after int64, limit int) (out InboxPage, err error) {
	if after < 0 || limit < 1 || limit > 100 {
		return out, ErrInvalid
	}
	err = s.Store.View(ctx, func(tx Tx) error {
		source, e := tx.Source(id)
		if e != nil || source.System || source.Kind != "manual" || !EqualKey(source.KeyHash, key) {
			return ErrUnauthorized
		}
		if !source.Enabled {
			return ErrDisabled
		}
		window, e := tx.Changes(after, limit, nil)
		if e != nil {
			return e
		}
		out = InboxPage{Deliveries: []Delivery{}, NextCursor: window.NextCursor, HighWatermark: window.HighWatermark, HasMore: window.HasMore}
		deliveries, e := recordsAs[Delivery](tx, "delivery")
		if e != nil {
			return e
		}
		for _, d := range deliveries {
			if d.Cursor > after && d.Cursor <= window.NextCursor && MatchesSource(id, d.TargetIDs) {
				out.Deliveries = append(out.Deliveries, d)
			}
		}
		sort.Slice(out.Deliveries, func(i, j int) bool { return out.Deliveries[i].Cursor < out.Deliveries[j].Cursor })
		return nil
	})
	return
}
