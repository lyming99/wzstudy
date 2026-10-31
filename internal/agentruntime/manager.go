package agentruntime

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"wzstudy/internal/study"
)

var (
	ErrNotConfigured  = errors.New("尚未配置采集端程序目录与工作路径")
	ErrAlreadyRunning = errors.New("采集端进程已在运行")
	ErrNotRunning     = errors.New("采集端进程未运行")
	ErrInvalidWorkDir = errors.New("工作路径无效：需要绝对路径")
	ErrInvalidInstall = errors.New("程序目录无效：需要绝对路径，且包含采集端程序与 agent.env")
	ErrInvalidModel   = errors.New("模型配置无效：请检查供应商、地址、模型名与密钥")
)

var validModelProviders = map[string]bool{"deepseek": true, "openai": true, "qwen": true, "custom": true}

type Status struct {
	Configured        bool       `json:"configured"`
	InstallDir        string     `json:"install_dir"`
	WorkDir           string     `json:"work_dir"`
	DefaultInstallDir string     `json:"default_install_dir"`
	DefaultWorkDir    string     `json:"default_work_dir"`
	AutoStart         bool       `json:"auto_start"`
	ModelProvider     string     `json:"model_provider"`
	ModelBaseURL      string     `json:"model_base_url"`
	ModelName         string     `json:"model_name"`
	HasModelAPIKey    bool       `json:"has_model_api_key"`
	Running           bool       `json:"running"`
	PID               int        `json:"pid,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	Binary            string     `json:"binary,omitempty"`
	Logs              []string   `json:"logs"`
}

// ModelSettings carries the model configuration saved with the runtime. The API
// key is stored per administrator workspace and is never returned by the API.
type ModelSettings struct {
	Provider    string
	BaseURL     string
	Model       string
	APIKey      string
	ClearAPIKey bool
}

// DefaultInstallDir is <installation directory>/agent unless AGENT_INSTALL_DIR
// points somewhere else. The launcher starts the server with the installation
// root as its working directory.
func DefaultInstallDir() string {
	if configured := strings.TrimSpace(os.Getenv("AGENT_INSTALL_DIR")); configured != "" && filepath.IsAbs(configured) {
		return configured
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Join(workingDirectory, "agent")
}

// DefaultWorkDir is <installation directory>/agent/work unless AGENT_WORK_DIR
// points somewhere else.
func DefaultWorkDir() string {
	if configured := strings.TrimSpace(os.Getenv("AGENT_WORK_DIR")); configured != "" && filepath.IsAbs(configured) {
		return configured
	}
	return filepath.Join(DefaultInstallDir(), "work")
}

// Manager owns at most one server-side agent process. Configuration is stored
// per administrator workspace; process lifetime is bound to the server process.
type Manager struct {
	lifecycle sync.Mutex // serialize start/stop/restart and configuration writes
	mu        sync.Mutex
	service   *study.Service
	logger    *slog.Logger
	settings  study.AgentRuntimeSettings
	cmd       *exec.Cmd
	done      chan struct{}
	startedAt time.Time
	lastError string
	logs      *ringBuffer
	logFile   *os.File
}

func New(service *study.Service, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{service: service, logger: logger, logs: newRing(200)}
}

func (m *Manager) Load(ctx context.Context) error {
	administrator, err := m.service.Administrator(ctx)
	if err != nil {
		return err
	}
	settings, err := m.service.AgentRuntimeSettings(study.WithWorkspace(ctx, administrator.ID))
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.settings = settings
	m.mu.Unlock()
	return nil
}

func (m *Manager) Settings() study.AgentRuntimeSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

func (m *Manager) UpdateSettings(ctx context.Context, installDir, workDir string, autoStart bool, model ModelSettings) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	installDir = strings.TrimSpace(installDir)
	workDir = strings.TrimSpace(workDir)
	if installDir == "" {
		installDir = DefaultInstallDir()
	}
	if workDir == "" {
		workDir = filepath.Join(installDir, "work")
	}
	if err := ValidateInstallDir(installDir); err != nil {
		return err
	}
	if !filepath.IsAbs(workDir) {
		return ErrInvalidWorkDir
	}
	model.Provider = strings.ToLower(strings.TrimSpace(model.Provider))
	model.BaseURL = strings.TrimSpace(model.BaseURL)
	model.Model = strings.TrimSpace(model.Model)
	model.APIKey = strings.TrimSpace(model.APIKey)
	if model.Provider != "" && !validModelProviders[model.Provider] {
		return ErrInvalidModel
	}
	if model.BaseURL != "" {
		parsed, err := url.Parse(model.BaseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || len(model.BaseURL) > 500 {
			return ErrInvalidModel
		}
	}
	if len(model.Model) > 120 || len(model.APIKey) > 500 || strings.ContainsAny(model.APIKey, "\r\n\x00") {
		return ErrInvalidModel
	}
	settings := study.AgentRuntimeSettings{
		InstallDir:    installDir,
		WorkDir:       workDir,
		AutoStart:     autoStart,
		ModelProvider: model.Provider,
		ModelBaseURL:  strings.TrimRight(model.BaseURL, "/"),
		ModelName:     model.Model,
		ModelAPIKey:   model.APIKey,
	}
	if model.APIKey == "" && !model.ClearAPIKey {
		settings.ModelAPIKey = m.SavedModelAPIKey()
	}
	if model.ClearAPIKey {
		settings.ModelAPIKey = ""
	}
	if err := m.service.SaveAgentRuntimeSettings(ctx, settings); err != nil {
		return err
	}
	m.mu.Lock()
	m.settings = settings
	m.mu.Unlock()
	return nil
}

func (m *Manager) SavedModelAPIKey() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings.ModelAPIKey
}

func (m *Manager) paths() (installDir, workDir string) {
	installDir = m.settings.InstallDir
	workDir = m.settings.WorkDir
	if installDir == "" {
		// Legacy records only stored the working directory, which also held the
		// executable and agent.env.
		installDir = workDir
	}
	if workDir == "" {
		workDir = installDir
	}
	return installDir, workDir
}

func agentBinaryName() string {
	if runtime.GOOS == "windows" {
		return "wzstudy-agent.exe"
	}
	return "wzstudy-agent"
}

func ValidateInstallDir(installDir string) error {
	if installDir == "" || !filepath.IsAbs(installDir) {
		return ErrInvalidInstall
	}
	info, err := os.Stat(installDir)
	if err != nil || !info.IsDir() {
		return ErrInvalidInstall
	}
	binary, err := os.Stat(filepath.Join(installDir, agentBinaryName()))
	if err != nil || !binary.Mode().IsRegular() {
		return ErrInvalidInstall
	}
	environment, err := os.Stat(filepath.Join(installDir, "agent.env"))
	if err != nil || !environment.Mode().IsRegular() {
		return ErrInvalidInstall
	}
	return nil
}

// ValidateWorkDir keeps the legacy layout check: the directory itself holds the
// executable and agent.env when no separate install directory is configured.
func ValidateWorkDir(workDir string) error {
	if workDir == "" || !filepath.IsAbs(workDir) {
		return ErrInvalidWorkDir
	}
	info, err := os.Stat(workDir)
	if err != nil || !info.IsDir() {
		return ErrInvalidWorkDir
	}
	binary, err := os.Stat(filepath.Join(workDir, agentBinaryName()))
	if err != nil || !binary.Mode().IsRegular() {
		return ErrInvalidWorkDir
	}
	environment, err := os.Stat(filepath.Join(workDir, "agent.env"))
	if err != nil || !environment.Mode().IsRegular() {
		return ErrInvalidWorkDir
	}
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	return m.start(ctx)
}

func (m *Manager) start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil {
		return ErrAlreadyRunning
	}
	installDir, workDir := m.paths()
	if installDir == "" || workDir == "" {
		return ErrNotConfigured
	}
	if err := ValidateInstallDir(installDir); err != nil {
		return err
	}
	if err := os.MkdirAll(workDir, 0700); err != nil {
		return err
	}
	binary := filepath.Join(installDir, agentBinaryName())
	command := exec.Command(binary, "run")
	command.Dir = workDir
	environment := []string{
		"AGENT_CONFIG=" + filepath.Join(installDir, "agent.env"),
		"AGENT_SKILLS_DIR=" + filepath.Join(installDir, "skills"),
	}
	if m.settings.ModelBaseURL != "" || m.settings.ModelName != "" || m.settings.ModelAPIKey != "" {
		baseURL := m.settings.ModelBaseURL
		if baseURL == "" {
			baseURL = "https://api.deepseek.com/v1"
		}
		modelName := m.settings.ModelName
		if modelName == "" {
			modelName = "deepseek-chat"
		}
		environment = append(environment, "AGENT_MODEL_BASE_URL="+baseURL, "AGENT_MODEL="+modelName)
		if m.settings.ModelAPIKey != "" {
			environment = append(environment, "AGENT_MODEL_API_KEY="+m.settings.ModelAPIKey)
		}
	}
	command.Env = append(os.Environ(), environment...)
	configureProcess(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(workDir, "agent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	m.cmd = command
	m.logFile = logFile
	m.startedAt = time.Now()
	m.lastError = ""
	m.done = make(chan struct{})
	done := m.done
	go m.scan(stdout)
	go m.scan(stderr)
	go func() {
		waitErr := command.Wait()
		m.mu.Lock()
		if m.cmd == command {
			m.cmd = nil
			if m.logFile != nil {
				_ = m.logFile.Close()
				m.logFile = nil
			}
			m.startedAt = time.Time{}
			if waitErr != nil {
				m.lastError = waitErr.Error()
			}
			if m.done == done {
				close(done)
				m.done = nil
			}
		}
		m.mu.Unlock()
	}()
	m.logger.Info("agent runtime started", "pid", command.Process.Pid, "install_dir", installDir, "work_dir", workDir)
	return nil
}

// Restart waits for the old process to exit before launching with saved settings.
// A stopped runtime is started; invalid paths never stop a healthy process.
func (m *Manager) Restart(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	installDir, workDir := m.paths()
	m.mu.Unlock()
	if installDir == "" || workDir == "" {
		return ErrNotConfigured
	}
	if err := ValidateInstallDir(installDir); err != nil {
		return err
	}
	if err := m.stop(ctx); err != nil && !errors.Is(err, ErrNotRunning) {
		return err
	}
	return m.start(ctx)
}

func (m *Manager) Stop(ctx context.Context) error {
	m.lifecycle.Lock()
	defer m.lifecycle.Unlock()
	return m.stop(ctx)
}

func (m *Manager) stop(ctx context.Context) error {
	m.mu.Lock()
	command := m.cmd
	done := m.done
	m.mu.Unlock()
	if command == nil || command.Process == nil {
		return ErrNotRunning
	}
	if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return context.DeadlineExceeded
		}
	}
	m.logger.Info("agent runtime stopped", "work_dir", m.Settings().WorkDir)
	return nil
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	installDir, workDir := m.paths()
	status := Status{
		Configured:        m.settings.InstallDir != "" || m.settings.WorkDir != "",
		InstallDir:        installDir,
		WorkDir:           workDir,
		DefaultInstallDir: DefaultInstallDir(),
		DefaultWorkDir:    DefaultWorkDir(),
		AutoStart:         m.settings.AutoStart,
		ModelProvider:     m.settings.ModelProvider,
		ModelBaseURL:      m.settings.ModelBaseURL,
		ModelName:         m.settings.ModelName,
		HasModelAPIKey:    m.settings.ModelAPIKey != "",
		Running:           m.cmd != nil,
		LastError:         m.lastError,
		Logs:              m.logs.snapshot(),
	}
	if m.cmd != nil && m.cmd.Process != nil {
		status.PID = m.cmd.Process.Pid
	}
	if !m.startedAt.IsZero() {
		started := m.startedAt
		status.StartedAt = &started
	}
	if installDir != "" {
		status.Binary = filepath.Join(installDir, agentBinaryName())
	}
	return status
}

func (m *Manager) scan(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		if len(line) > 4000 {
			line = line[:4000]
		}
		m.logs.add(line)
		m.mu.Lock()
		if m.logFile != nil {
			_, _ = m.logFile.WriteString(line + "\n")
		}
		m.mu.Unlock()
	}
}

type ringBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func newRing(maximum int) *ringBuffer {
	return &ringBuffer{max: maximum}
}

func (r *ringBuffer) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	if len(r.lines) > r.max {
		r.lines = r.lines[len(r.lines)-r.max:]
	}
}

func (r *ringBuffer) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}
