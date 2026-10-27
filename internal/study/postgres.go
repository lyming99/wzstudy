package study

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ Pool *pgxpool.Pool }

func OpenPostgres(ctx context.Context, dsn string) (*PostgresStore, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 16
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresStore{Pool: pool}, nil
}
func (s *PostgresStore) Ping(ctx context.Context) error {
	if err := s.Pool.Ping(ctx); err != nil {
		return err
	}
	var clock int64
	if err := s.Pool.QueryRow(ctx, `SELECT cursor FROM feed_clock WHERE id=1`).Scan(&clock); err != nil {
		return err
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM workspace_records WHERE false`).Scan(&count); err != nil {
		return err
	}
	var ready bool
	if err := s.Pool.QueryRow(ctx, `SELECT to_regclass('workspace_client_key_hash') IS NOT NULL`).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return errors.New("identity schema migration required")
	}
	var feedbackReady bool
	if err := s.Pool.QueryRow(ctx, `SELECT to_regclass('workspace_feedback_state_client_item') IS NOT NULL`).Scan(&feedbackReady); err != nil {
		return err
	}
	if !feedbackReady {
		return errors.New("client feedback schema migration required")
	}
	var catalogReady bool
	if err := s.Pool.QueryRow(ctx, `SELECT to_regclass('workspace_catalog_change_cursor') IS NOT NULL`).Scan(&catalogReady); err != nil {
		return err
	}
	if !catalogReady {
		return errors.New("catalog collection schema migration required")
	}
	var learningReady bool
	if err := s.Pool.QueryRow(ctx, `SELECT to_regclass('workspace_learning_state_client_item') IS NOT NULL`).Scan(&learningReady); err != nil {
		return err
	}
	if !learningReady {
		return errors.New("learning data schema migration required")
	}
	return nil
}
func (s *PostgresStore) Close() { s.Pool.Close() }
func (s *PostgresStore) View(ctx context.Context, fn func(Tx) error) error {
	return s.run(ctx, false, fn)
}
func (s *PostgresStore) Update(ctx context.Context, fn func(Tx) error) error {
	return s.run(ctx, true, fn)
}
func (s *PostgresStore) run(ctx context.Context, write bool, fn func(Tx) error) error {
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	if write {
		options = pgx.TxOptions{IsoLevel: pgx.ReadCommitted}
	}
	tx, err := s.Pool.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if write {
		var clock int64
		if err = tx.QueryRow(ctx, `SELECT cursor FROM feed_clock WHERE id=1 FOR UPDATE`).Scan(&clock); err != nil {
			return err
		}
	}
	if err = fn(&postgresTx{ctx: ctx, tx: tx}); err != nil {
		return dbError(err)
	}
	return dbError(tx.Commit(ctx))
}
func dbError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && (pg.Code == "23505" || pg.Code == "40001") {
		return ErrConflict
	}
	return err
}

type postgresTx struct {
	ctx context.Context
	tx  pgx.Tx
}
type sourceSecret struct {
	KeyHash      string    `json:"key_hash"`
	ETag         string    `json:"etag"`
	LastModified string    `json:"last_modified"`
	PollToken    string    `json:"poll_token"`
	PollUntil    time.Time `json:"poll_until"`
}
type sourceRecord struct {
	Source Source       `json:"source"`
	Secret sourceSecret `json:"internal"`
}

func encodeSource(v Source) ([]byte, error) {
	return json.Marshal(sourceRecord{Source: v, Secret: sourceSecret{KeyHash: v.KeyHash, ETag: v.ETag, LastModified: v.LastModified, PollToken: v.PollToken, PollUntil: v.PollUntil}})
}
func decodeSource(b []byte) (Source, error) {
	var r sourceRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return Source{}, err
	}
	v := r.Source
	v.KeyHash = r.Secret.KeyHash
	v.ETag = r.Secret.ETag
	v.LastModified = r.Secret.LastModified
	v.PollToken = r.Secret.PollToken
	v.PollUntil = r.Secret.PollUntil
	return v, nil
}
func (t *postgresTx) Sources() ([]Source, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT record FROM sources ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		v, e := decodeSource(b)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (t *postgresTx) Source(id string) (Source, error) {
	var b []byte
	if err := t.tx.QueryRow(t.ctx, `SELECT record FROM sources WHERE id=$1`, id).Scan(&b); err != nil {
		return Source{}, dbError(err)
	}
	return decodeSource(b)
}
func (t *postgresTx) SaveSource(v Source) error {
	b, err := encodeSource(v)
	if err != nil {
		return err
	}
	_, err = t.tx.Exec(t.ctx, `INSERT INTO sources(id,slug,record) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET slug=excluded.slug,record=excluded.record`, v.ID, v.Slug, b)
	return dbError(err)
}
func (t *postgresTx) Item(id string) (Item, error) {
	var b []byte
	err := t.tx.QueryRow(t.ctx, `SELECT record FROM items WHERE id=$1`, id).Scan(&b)
	if err != nil {
		return Item{}, dbError(err)
	}
	var v Item
	err = json.Unmarshal(b, &v)
	return v, err
}
func (t *postgresTx) FindItem(source, external string) (Item, error) {
	var b []byte
	err := t.tx.QueryRow(t.ctx, `SELECT record FROM items WHERE source_id=$1 AND external_id=$2`, source, external).Scan(&b)
	if err != nil {
		return Item{}, dbError(err)
	}
	var v Item
	err = json.Unmarshal(b, &v)
	return v, err
}
func (t *postgresTx) SaveItem(v Item) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var live []byte
	var publishedAt *time.Time
	if v.Published != nil {
		live, err = json.Marshal(v.Published)
		if err != nil {
			return err
		}
		publishedAt = v.Published.PublishedAt
	}
	_, err = t.tx.Exec(t.ctx, `INSERT INTO items(id,source_id,external_id,record,live,published_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO UPDATE SET record=excluded.record,live=excluded.live,published_at=excluded.published_at,updated_at=excluded.updated_at`, v.ID, v.SourceID, v.ExternalID, b, live, publishedAt, v.UpdatedAt)
	return dbError(err)
}
func (t *postgresTx) Cursor() (int64, error) {
	var n int64
	err := t.tx.QueryRow(t.ctx, `SELECT cursor FROM feed_clock WHERE id=1`).Scan(&n)
	return n, err
}
func (t *postgresTx) Append(v Change) (Change, error) {
	err := t.tx.QueryRow(t.ctx, `UPDATE feed_clock SET cursor=cursor+1 WHERE id=1 RETURNING cursor`).Scan(&v.Cursor)
	if err != nil {
		return v, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v, err
	}
	_, err = t.tx.Exec(t.ctx, `INSERT INTO changes(cursor,source_id,item_id,record) VALUES($1,$2,$3,$4)`, v.Cursor, v.SourceID, v.ItemID, b)
	return v, err
}
func (t *postgresTx) Items(f Filter) ([]Item, error) {
	if ValidateFeedbackExclusions(f.FeedbackExclude) != nil {
		return nil, ErrInvalid
	}
	column := "live"
	where := []string{"live IS NOT NULL", "record->>'deleted_at' IS NULL"}
	if f.Admin {
		column = "record->'draft'"
		where = []string{"record->>'deleted_at' IS NULL"}
	}
	args := []any{}
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if f.Kind != "" {
		where = append(where, "COALESCE(NULLIF("+column+"->>'kind',''),'article')="+arg(f.Kind))
	}
	if f.Admin && f.Status != "" {
		where = append(where, "COALESCE(record->>'status',CASE WHEN live IS NULL OR (record->>'revision')::bigint <> (live->>'revision')::bigint THEN 'pending' ELSE 'published' END)="+arg(f.Status))
	}
	if len(f.SourceIDs) > 0 {
		where = append(where, "source_id = ANY("+arg(f.SourceIDs)+"::text[])")
	}
	if f.Category != "" {
		where = append(where, column+"->>'category' = "+arg(f.Category))
	}
	if f.Tag != "" {
		where = append(where, "("+column+"->'tags') ? "+arg(f.Tag))
	}
	if f.Query != "" {
		where = append(where, "strpos(lower(("+column+"->>'title') || ' ' || ("+column+"->>'summary') || ' ' || COALESCE("+column+"->>'author','') || ' ' || COALESCE("+column+"->>'category','') || ' ' || COALESCE(("+column+"->'tags')::text,'')),lower("+arg(f.Query)+")) > 0")
	}
	if !f.Admin && f.FeedbackClientID != "" && len(f.FeedbackExclude) > 0 {
		clientArg := arg(f.FeedbackClientID)
		sinceArg := ""
		if f.FeedbackSince != nil {
			sinceArg = arg(*f.FeedbackSince)
		}
		condition := feedbackSQLConditions("fs", f.FeedbackExclude, sinceArg)
		where = append(where, "NOT EXISTS (SELECT 1 FROM workspace_records fs WHERE fs.kind='feedback_state' AND fs.record->>'client_id'="+clientArg+" AND fs.record->>'item_id'=items.id AND ("+condition+"))")
	}
	order := "published_at DESC,id DESC"
	if f.Admin {
		order = "updated_at DESC,id DESC"
	}
	if f.Ascending {
		order = strings.ReplaceAll(order, "DESC", "ASC")
	}
	query := "SELECT record FROM items WHERE " + strings.Join(where, " AND ") + " ORDER BY " + order + " LIMIT " + arg(f.Limit) + " OFFSET " + arg(f.Offset)
	rows, err := t.tx.Query(t.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var b []byte
		var v Item
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (t *postgresTx) Changes(after int64, limit int, ids []string) (ChangePage, error) {
	high, err := t.Cursor()
	page := ChangePage{Changes: []Change{}, NextCursor: after, HighWatermark: high}
	if err != nil {
		return page, err
	}
	if after > high {
		return page, ErrInvalid
	}
	// Scan a bounded GLOBAL window, then filter. Even an empty matching page advances
	// its cursor, avoiding starvation when other sources dominate the stream.
	rows, err := t.tx.Query(t.ctx, `SELECT record FROM changes WHERE cursor>$1 ORDER BY cursor LIMIT $2`, after, limit)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		var v Change
		if err = rows.Scan(&b); err != nil {
			return page, err
		}
		if err = json.Unmarshal(b, &v); err != nil {
			return page, err
		}
		page.NextCursor = v.Cursor
		if MatchesSource(v.SourceID, ids) {
			page.Changes = append(page.Changes, v)
		}
	}
	page.HasMore = page.NextCursor < high
	return page, rows.Err()
}

func (t *postgresTx) Records(kind string) ([]json.RawMessage, error) {
	rows, e := t.tx.Query(t.ctx, `SELECT record FROM workspace_records WHERE kind=$1 ORDER BY id`, kind)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}
func (t *postgresTx) Record(kind, id string) (json.RawMessage, error) {
	var b []byte
	e := t.tx.QueryRow(t.ctx, `SELECT record FROM workspace_records WHERE kind=$1 AND id=$2`, kind, id).Scan(&b)
	return json.RawMessage(b), dbError(e)
}
func (t *postgresTx) SaveRecord(kind, id string, b json.RawMessage) error {
	_, e := t.tx.Exec(t.ctx, `INSERT INTO workspace_records(kind,id,record) VALUES($1,$2,$3) ON CONFLICT(kind,id) DO UPDATE SET record=excluded.record`, kind, id, []byte(b))
	return dbError(e)
}
func (t *postgresTx) DeleteRecord(kind, id string) error {
	_, e := t.tx.Exec(t.ctx, `DELETE FROM workspace_records WHERE kind=$1 AND id=$2`, kind, id)
	return dbError(e)
}
