package study

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const (
	maximumReleaseNotes = 2000
	maximumReleaseSize  = 200 << 20
)

var releaseVersionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)
var releaseSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var releasePlatforms = map[string]bool{"windows": true, "linux": true, "macos": true}
var releaseArchitectures = map[string]bool{"amd64": true, "arm64": true}
var releaseExtensions = []string{".exe", ".zip", ".msi", ".tar.gz", ".tgz"}

// AgentRelease is a downloadable collection-agent package published by an
// administrator. Bytes live under DATA_DIR/agent-releases/<id>; only metadata
// is stored as a workspace record.
type AgentRelease struct {
	OwnerID   string    `json:"owner_id,omitempty"`
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	Platform  string    `json:"platform"`
	Arch      string    `json:"arch"`
	Filename  string    `json:"filename"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	Notes     string    `json:"notes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AgentReleaseInput struct {
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Arch     string `json:"arch"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Notes    string `json:"notes"`
}

func normalizeAgentRelease(in AgentReleaseInput) (AgentReleaseInput, error) {
	in.Version = strings.TrimSpace(in.Version)
	in.Platform = strings.ToLower(strings.TrimSpace(in.Platform))
	in.Arch = strings.ToLower(strings.TrimSpace(in.Arch))
	in.Filename = path.Base(strings.ReplaceAll(strings.TrimSpace(in.Filename), "\\", "/"))
	in.SHA256 = strings.ToLower(strings.TrimSpace(in.SHA256))
	in.Notes = strings.TrimSpace(in.Notes)
	if !releaseVersionPattern.MatchString(in.Version) || len(in.Version) > 64 {
		return in, ErrInvalid
	}
	if !releasePlatforms[in.Platform] || !releaseArchitectures[in.Arch] {
		return in, ErrInvalid
	}
	if in.Filename == "." || in.Filename == "" || len(in.Filename) > 255 || strings.IndexFunc(in.Filename, unicode.IsControl) >= 0 {
		return in, ErrInvalid
	}
	lower := strings.ToLower(in.Filename)
	allowed := false
	for _, extension := range releaseExtensions {
		if strings.HasSuffix(lower, extension) {
			allowed = true
			break
		}
	}
	if !allowed {
		return in, ErrInvalid
	}
	if in.Size < 1 || in.Size > maximumReleaseSize {
		return in, ErrInvalid
	}
	if !releaseSHA256Pattern.MatchString(in.SHA256) {
		return in, ErrInvalid
	}
	if !validText(in.Notes, maximumReleaseNotes) {
		return in, ErrInvalid
	}
	return in, nil
}

func (s *Service) AgentReleases(ctx context.Context) (out []AgentRelease, err error) {
	out = []AgentRelease{}
	err = s.Store.View(ctx, func(tx Tx) error {
		all, e := recordsAs[AgentRelease](tx, "agent_release")
		if e != nil {
			return e
		}
		sort.Slice(all, func(i, j int) bool {
			if all[i].CreatedAt.Equal(all[j].CreatedAt) {
				return all[i].ID > all[j].ID
			}
			return all[i].CreatedAt.After(all[j].CreatedAt)
		})
		out = all
		return nil
	})
	return
}

func (s *Service) AgentRelease(ctx context.Context, id string) (out AgentRelease, err error) {
	err = s.Store.View(ctx, func(tx Tx) error { var e error; out, e = recordAs[AgentRelease](tx, "agent_release", id); return e })
	return
}

func (s *Service) CreateAgentRelease(ctx context.Context, in AgentReleaseInput) (out AgentRelease, err error) {
	in, err = normalizeAgentRelease(in)
	if err != nil {
		return
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		all, e := recordsAs[AgentRelease](tx, "agent_release")
		if e != nil {
			return e
		}
		for _, other := range all {
			if other.Version == in.Version && other.Platform == in.Platform && other.Arch == in.Arch {
				return ErrConflict
			}
		}
		now := s.Now()
		out = AgentRelease{
			OwnerID: WorkspaceUserID(ctx), ID: uuid.NewString(), Version: in.Version,
			Platform: in.Platform, Arch: in.Arch, Filename: in.Filename, Size: in.Size,
			SHA256: in.SHA256, Notes: in.Notes, CreatedAt: now, UpdatedAt: now,
		}
		return recordSave(tx, "agent_release", out.ID, out)
	})
	return
}

func (s *Service) DeleteAgentRelease(ctx context.Context, id string) (out AgentRelease, err error) {
	err = s.Store.Update(ctx, func(tx Tx) error {
		v, e := recordAs[AgentRelease](tx, "agent_release", id)
		if e != nil {
			return e
		}
		out = v
		return tx.DeleteRecord("agent_release", id)
	})
	return
}
