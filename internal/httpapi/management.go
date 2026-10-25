package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"wzstudy/internal/study"
)

func (s *Server) sameOrigin(r *http.Request, origin string) bool {
	u, e := url.Parse(origin)
	if e != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return u.Scheme == scheme && strings.EqualFold(u.Host, r.Host)
}
func (s *Server) workspace(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.Workspace(r.Context())
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var in study.WorkspaceSettings
	if !decode(w, r, &in) {
		return
	}
	v, e := s.service.SaveSettings(r.Context(), in)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) saveItem(w http.ResponseWriter, r *http.Request) {
	var in study.ManagedInput
	if !decode(w, r, &in) {
		return
	}
	id := chi.URLParam(r, "id")
	v, e := s.service.SaveManaged(r.Context(), id, in, nil)
	if e != nil {
		s.fail(w, e)
		return
	}
	status := 200
	if id == "" {
		status = 201
	}
	success(w, status, v)
}
func (s *Server) submitItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.service.Submit(r.Context(), chi.URLParam(r, "id"), in.Revision)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) deleteItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	if e := s.service.DeleteItem(r.Context(), chi.URLParam(r, "id"), in.Revision); e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, map[string]bool{"deleted": true})
}
func (s *Server) saveCollection(w http.ResponseWriter, r *http.Request) {
	var in study.Collection
	if !decode(w, r, &in) {
		return
	}
	v, e := s.service.SaveCollection(r.Context(), chi.URLParam(r, "id"), in)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	if e := s.service.DeleteCollection(r.Context(), chi.URLParam(r, "id"), in.Revision); e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, map[string]bool{"deleted": true})
}
func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title       string    `json:"title"`
		Items       []string  `json:"item_ids"`
		Targets     []string  `json:"target_ids"`
		ScheduledAt time.Time `json:"scheduled_at"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.service.CreatePlan(r.Context(), study.PushPlan{Title: in.Title, ItemIDs: in.Items, TargetIDs: in.Targets, ScheduledAt: in.ScheduledAt})
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 201, v)
}
func (s *Server) planAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64  `json:"revision"`
		Action   string `json:"action"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, e := s.service.PlanAction(r.Context(), chi.URLParam(r, "id"), in.Revision, in.Action)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}
func (s *Server) inbox(w http.ResponseWriter, r *http.Request) {
	after, limit, _, e := changeArgs(r, false)
	if e != nil {
		s.fail(w, e)
		return
	}
	v, e := s.service.Inbox(r.Context(), chi.URLParam(r, "id"), bearer(r), after, limit)
	if e != nil {
		s.fail(w, e)
		return
	}
	success(w, 200, v)
}

func strictMetadata(raw string, v any) error {
	if len(raw) > 1<<20 || !utf8.ValidString(raw) || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return study.ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return study.ErrInvalid
	}
	var rest any
	if e := d.Decode(&rest); e != io.EOF {
		return study.ErrInvalid
	}
	return nil
}
func uploadMIME(name string, header []byte) string {
	typ := http.DetectContentType(header)
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".mp3":
		if typ == "audio/mpeg" {
			return typ
		}
	case ".wav":
		if typ == "audio/wave" || typ == "audio/x-wav" {
			return "audio/wav"
		}
	case ".ogg", ".oga":
		if bytes.HasPrefix(header, []byte("OggS")) {
			return "audio/ogg"
		}
	case ".flac":
		if bytes.HasPrefix(header, []byte("fLaC")) {
			return "audio/flac"
		}
	case ".m4a":
		if len(header) > 12 && string(header[4:8]) == "ftyp" {
			return "audio/mp4"
		}
	case ".mp4", ".m4v", ".mov":
		if typ == "video/mp4" || typ == "video/quicktime" {
			return typ
		}
	case ".webm":
		if typ == "video/webm" {
			return typ
		}
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		if typ == "image/png" || typ == "image/jpeg" || typ == "image/gif" || typ == "image/webp" {
			return typ
		}
	case ".txt", ".md", ".markdown":
		// A 512-byte sniff may end in the middle of a multi-byte character.
		// Full UTF-8 validation is performed after reading the complete text file.
		sample := header
		for trim := 0; trim < 3 && !utf8.Valid(sample) && len(sample) > 0; trim++ {
			sample = sample[:len(sample)-1]
		}
		if bytes.IndexByte(header, 0) < 0 && utf8.Valid(sample) {
			if ext != ".txt" {
				return "text/markdown"
			}
			return "text/plain"
		}
	}
	return ""
}
func (s *Server) readUploads(w http.ResponseWriter, r *http.Request) (assets []study.Asset, cleanup func(), err error) {
	assets = []study.Asset{}
	paths := []string{}
	cleanup = func() {
		for _, p := range paths {
			_ = os.Remove(p)
		}
	}
	typ, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || typ != "multipart/form-data" {
		return assets, cleanup, study.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes+(2<<20))
	if e = r.ParseMultipartForm(2 << 20); e != nil {
		return assets, cleanup, e
	}
	files := r.MultipartForm.File["files"]
	if len(files) < 1 || len(files) > 20 {
		return assets, cleanup, study.ErrInvalid
	}
	dir := filepath.Join(s.cfg.DataDir, "uploads")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return assets, cleanup, e
	}
	var total int64
	for _, header := range files {
		name := path.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
		if name == "." || len(name) > 255 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return assets, cleanup, study.ErrInvalid
		}
		file, e := header.Open()
		if e != nil {
			return assets, cleanup, e
		}
		prefix := make([]byte, 512)
		n, e := io.ReadFull(file, prefix)
		if e != nil && e != io.ErrUnexpectedEOF && e != io.EOF {
			file.Close()
			return assets, cleanup, e
		}
		prefix = prefix[:n]
		typ := uploadMIME(name, prefix)
		if typ == "" || n == 0 {
			file.Close()
			return assets, cleanup, study.ErrInvalid
		}
		id := uuid.NewString()
		dst := filepath.Join(dir, id)
		out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			file.Close()
			return assets, cleanup, e
		}
		paths = append(paths, dst)
		size, copyErr := io.Copy(out, io.LimitReader(io.MultiReader(bytes.NewReader(prefix), file), s.cfg.MaxUploadBytes+1))
		syncErr := out.Sync()
		closeErr := out.Close()
		file.Close()
		if copyErr != nil {
			return assets, cleanup, copyErr
		}
		if syncErr != nil {
			return assets, cleanup, syncErr
		}
		if closeErr != nil {
			return assets, cleanup, closeErr
		}
		total += size
		if size < 1 || total > s.cfg.MaxUploadBytes {
			return assets, cleanup, &http.MaxBytesError{Limit: s.cfg.MaxUploadBytes}
		}
		assets = append(assets, study.Asset{ID: id, Name: name, MIME: typ, Size: size, CreatedAt: time.Now().UTC()})
	}
	return assets, cleanup, nil
}
func (s *Server) uploadFailure(w http.ResponseWriter, e error) {
	var tooLarge *http.MaxBytesError
	if errors.As(e, &tooLarge) {
		failure(w, 413, "payload_too_large", "上传内容超过服务端大小限制")
		return
	}
	if errors.Is(e, study.ErrInvalid) {
		failure(w, 400, "invalid_upload", "文件格式、数量或资料元数据无效；仅接受音视频、文本与非 SVG 图片")
		return
	}
	s.fail(w, e)
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	admin := chi.URLParam(r, "id") == ""
	sourceID := chi.URLParam(r, "id")
	if !admin {
		if _, e := s.service.AuthorizeSource(r.Context(), sourceID, bearer(r), study.PermissionIngest, study.PermissionUpload); e != nil {
			s.fail(w, e)
			return
		}
	}
	assets, cleanup, e := s.readUploads(w, r)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if e != nil {
		cleanup()
		s.uploadFailure(w, e)
		return
	}
	committed := false
	defer func() {
		if !committed {
			cleanup()
		}
	}()
	var in study.ManagedInput
	var c study.Content
	if admin {
		e = strictMetadata(r.FormValue("metadata"), &in)
		c = in.Content
	} else {
		e = strictMetadata(r.FormValue("metadata"), &c)
	}
	if e != nil {
		s.uploadFailure(w, e)
		return
	}
	c.Assets = assets
	if c.ExternalID == "" {
		c.ExternalID = uuid.NewString()
	}
	if study.Kind(c) == "article" && len(assets) == 1 && (assets[0].MIME == "text/plain" || assets[0].MIME == "text/markdown") {
		if assets[0].Size > 262144 {
			s.uploadFailure(w, study.ErrInvalid)
			return
		}
		text, e := os.ReadFile(filepath.Join(s.cfg.DataDir, "uploads", assets[0].ID))
		if e != nil || !utf8.Valid(text) {
			s.uploadFailure(w, study.ErrInvalid)
			return
		}
		c.Content = string(text)
		if assets[0].MIME == "text/markdown" {
			c.Format = "markdown"
		}
	}
	if admin {
		in.Content = c
		v, e := s.service.SaveManaged(r.Context(), "", in, assets)
		if e != nil {
			// A lost commit acknowledgement does not prove rollback. Keep immutable
			// bytes on uncertain storage errors rather than break a committed record.
			committed = retainUploadOnUncertainCommit(e)
			s.fail(w, e)
			return
		}
		committed = true
		success(w, 201, v)
	} else {
		v, e := s.service.IngestAssets(r.Context(), sourceID, bearer(r), c, assets)
		if e != nil {
			committed = retainUploadOnUncertainCommit(e)
			s.fail(w, e)
			return
		}
		committed = true
		success(w, 201, v)
	}
}
func retainUploadOnUncertainCommit(err error) bool {
	for _, known := range []error{study.ErrInvalid, study.ErrConflict, study.ErrUnauthorized, study.ErrDisabled, study.ErrForbidden, study.ErrNotFound, study.ErrBusy} {
		if errors.Is(err, known) {
			return false
		}
	}
	return true
}
func (s *Server) media(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, e := uuid.Parse(id); e != nil {
		s.fail(w, study.ErrNotFound)
		return
	}
	asset, e := s.service.Asset(r.Context(), id, s.isAdmin(r))
	if e != nil {
		s.fail(w, e)
		return
	}
	f, e := os.Open(filepath.Join(s.cfg.DataDir, "uploads", asset.ID))
	if e != nil {
		s.fail(w, study.ErrNotFound)
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		s.fail(w, e)
		return
	}
	w.Header().Set("Content-Type", asset.MIME)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": asset.Name}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", "\""+asset.ID+"\"")
	http.ServeContent(w, r, asset.Name, info.ModTime(), f)
}
