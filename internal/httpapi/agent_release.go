package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"wzstudy/internal/study"
)

const maximumAgentReleaseBytes = 64 << 20

func (s *Server) releaseFailure(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		failure(w, 413, "payload_too_large", "程序包超过大小限制（最大 64 MiB）")
		return
	}
	if errors.Is(err, study.ErrInvalid) {
		failure(w, 400, "invalid_release", "版本、平台、架构、文件类型或校验值无效")
		return
	}
	s.fail(w, err)
}

func (s *Server) adminAgentReleases(w http.ResponseWriter, r *http.Request) {
	releases, err := s.service.AgentReleases(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	success(w, 200, releases)
}

// adminUploadAgentRelease stores the package bytes first, then records metadata
// in a workspace transaction. A failed commit removes the orphaned file.
func (s *Server) adminUploadAgentRelease(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maximumAgentReleaseBytes+(4<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.releaseFailure(w, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		s.releaseFailure(w, study.ErrInvalid)
		return
	}
	defer file.Close()
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(header.Filename), "\\", "/"))
	if name == "." || name == "" || len(name) > 255 {
		s.releaseFailure(w, study.ErrInvalid)
		return
	}
	id := uuid.NewString()
	directory := filepath.Join(s.cfg.DataDir, "agent-releases")
	if err := os.MkdirAll(directory, 0700); err != nil {
		s.fail(w, err)
		return
	}
	target := filepath.Join(directory, id+".upload")
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		s.fail(w, err)
		return
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hasher), io.LimitReader(file, maximumAgentReleaseBytes+1))
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(target)
		s.fail(w, study.ErrInvalid)
		return
	}
	if written > maximumAgentReleaseBytes {
		_ = os.Remove(target)
		failure(w, 413, "payload_too_large", "程序包超过大小限制（最大 64 MiB）")
		return
	}
	release, err := s.service.CreateAgentRelease(r.Context(), study.AgentReleaseInput{
		Version:  r.FormValue("version"),
		Platform: r.FormValue("platform"),
		Arch:     r.FormValue("arch"),
		Filename: name,
		Size:     written,
		SHA256:   hex.EncodeToString(hasher.Sum(nil)),
		Notes:    r.FormValue("notes"),
	})
	if err != nil {
		_ = os.Remove(target)
		s.releaseFailure(w, err)
		return
	}
	// The record owns the final identifier, so move the verified bytes into
	// place only after the metadata transaction commits.
	if err := os.Rename(target, filepath.Join(directory, release.ID)); err != nil {
		_, _ = s.service.DeleteAgentRelease(r.Context(), release.ID)
		_ = os.Remove(target)
		s.fail(w, err)
		return
	}
	success(w, 201, release)
}

func (s *Server) adminDownloadAgentRelease(w http.ResponseWriter, r *http.Request) {
	release, err := s.service.AgentRelease(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	file, err := os.Open(filepath.Join(s.cfg.DataDir, "agent-releases", release.ID))
	if err != nil {
		s.fail(w, study.ErrNotFound)
		return
	}
	defer file.Close()
	download := strings.Map(func(character rune) rune {
		if character == '"' || character == '\\' || character == '\r' || character == '\n' {
			return -1
		}
		return character
	}, release.Filename)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+download+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, release.Filename, release.CreatedAt, file)
}

func (s *Server) adminDeleteAgentRelease(w http.ResponseWriter, r *http.Request) {
	release, err := s.service.DeleteAgentRelease(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	_ = os.Remove(filepath.Join(s.cfg.DataDir, "agent-releases", release.ID))
	w.WriteHeader(http.StatusNoContent)
}
