package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the complete agent-side configuration. Model credentials never
// leave this process; the server only sees collection tasks and materials.
type Config struct {
	ServerURL     string
	AgentKey      string
	ModelBase     string
	ModelKey      string
	Model         string
	SkillsDir     string
	WorkDir       string
	PollEvery     time.Duration
	WaitSeconds   int
	MaxFetch      int64
	MediaMaxBytes int64
	YtDlpPath     string
	AllowLocal    bool
	LogLevel      string
	Version       string
}

func Load() (Config, error) {
	values, err := loadConfigFile()
	if err != nil {
		return Config{}, err
	}
	return FromEnv(func(key string) string {
		if value := os.Getenv(key); value != "" {
			return value
		}
		return values[key]
	})
}

// loadConfigFile reads an optional KEY=VALUE file so a packaged agent can be
// started by editing one file instead of exporting environment variables.
// AGENT_CONFIG selects the path; otherwise ./agent.env is used when present.
// Process environment variables always take precedence.
func loadConfigFile() (map[string]string, error) {
	path := strings.TrimSpace(os.Getenv("AGENT_CONFIG"))
	if path == "" {
		if _, err := os.Stat("agent.env"); err == nil {
			path = "agent.env"
		}
	}
	if path == "" {
		return map[string]string{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values, nil
}

func FromEnv(get func(string) string) (c Config, err error) {
	value := func(key, fallback string) string {
		if v := strings.TrimSpace(get(key)); v != "" {
			return v
		}
		return fallback
	}
	c = Config{
		ServerURL: value("WZSTUDY_SERVER_URL", ""),
		AgentKey:  value("WZSTUDY_AGENT_KEY", ""),
		ModelBase: value("AGENT_MODEL_BASE_URL", "https://api.openai.com/v1"),
		ModelKey:  strings.TrimSpace(get("AGENT_MODEL_API_KEY")),
		Model:     value("AGENT_MODEL", ""),
		SkillsDir: value("AGENT_SKILLS_DIR", "skills"),
		WorkDir:   value("AGENT_WORK_DIR", ".state"),
		LogLevel:  strings.ToLower(value("AGENT_LOG_LEVEL", "info")),
		Version:   value("AGENT_VERSION", "0.1.0"),
		YtDlpPath: strings.TrimSpace(get("AGENT_YTDLP_PATH")),
	}
	if c.ServerURL == "" || c.AgentKey == "" || c.Model == "" {
		return c, errors.New("WZSTUDY_SERVER_URL, WZSTUDY_AGENT_KEY and AGENT_MODEL are required")
	}
	if !strings.HasPrefix(c.AgentKey, "wzag_") {
		return c, errors.New("WZSTUDY_AGENT_KEY must be an agent key (wzag_...)")
	}
	server, err := url.Parse(c.ServerURL)
	if err != nil || (server.Scheme != "http" && server.Scheme != "https") || server.Host == "" || server.User != nil {
		return c, errors.New("WZSTUDY_SERVER_URL must be an absolute http(s) URL")
	}
	if server.RawQuery != "" || server.Fragment != "" {
		return c, errors.New("WZSTUDY_SERVER_URL must not contain a query or fragment")
	}
	model, err := url.Parse(c.ModelBase)
	if err != nil || (model.Scheme != "http" && model.Scheme != "https") || model.Host == "" {
		return c, errors.New("AGENT_MODEL_BASE_URL must be an absolute http(s) URL")
	}
	poll, err := integer(value("AGENT_POLL_SECONDS", "5"), 1, 300)
	if err != nil {
		return c, fmt.Errorf("AGENT_POLL_SECONDS: %w", err)
	}
	c.PollEvery = time.Duration(poll) * time.Second
	if c.WaitSeconds, err = integer(value("AGENT_WAIT_SECONDS", "20"), 0, 25); err != nil {
		return c, fmt.Errorf("AGENT_WAIT_SECONDS: %w", err)
	}
	maxFetch, err := integer(value("AGENT_MAX_FETCH_BYTES", "2097152"), 65536, 16<<20)
	if err != nil {
		return c, fmt.Errorf("AGENT_MAX_FETCH_BYTES: %w", err)
	}
	c.MaxFetch = int64(maxFetch)
	mediaMax, err := integer(value("AGENT_MEDIA_MAX_BYTES", "209715200"), 1<<20, 1<<30)
	if err != nil {
		return c, fmt.Errorf("AGENT_MEDIA_MAX_BYTES: %w", err)
	}
	c.MediaMaxBytes = int64(mediaMax)
	if c.YtDlpPath != "" {
		if c.YtDlpPath, err = filepath.Abs(c.YtDlpPath); err != nil {
			return c, errors.New("AGENT_YTDLP_PATH must be a valid path")
		}
	}
	c.AllowLocal, err = strconv.ParseBool(value("AGENT_ALLOW_PRIVATE_NETWORK", "false"))
	if err != nil {
		return c, errors.New("AGENT_ALLOW_PRIVATE_NETWORK must be a boolean")
	}
	if c.SkillsDir != "" {
		if c.SkillsDir, err = filepath.Abs(c.SkillsDir); err != nil {
			return c, errors.New("AGENT_SKILLS_DIR must be a valid path")
		}
	}
	if c.WorkDir, err = filepath.Abs(c.WorkDir); err != nil {
		return c, errors.New("AGENT_WORK_DIR must be a valid path")
	}
	return c, nil
}

func integer(value string, minimum, maximum int) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("must be an integer in [%d,%d]", minimum, maximum)
	}
	return parsed, nil
}
