package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"wzstudy-agent/internal/collect"
	"wzstudy-agent/internal/config"
	"wzstudy-agent/internal/loop"
	"wzstudy-agent/internal/provider"
	"wzstudy-agent/internal/serverapi"
	"wzstudy-agent/internal/skill"
	"wzstudy-agent/internal/tool"
	"wzstudy-agent/internal/worker"
)

var version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(run(false))
	case "once":
		os.Exit(run(true))
	case "doctor":
		os.Exit(doctor())
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `wzstudy-agent %s

用法：
  wzstudy-agent run       常驻运行，循环领取服务端指派的采集任务
  wzstudy-agent once      领取并执行一个任务后退出（适合容器 job / cron）
  wzstudy-agent doctor    校验配置、服务端连通性与采集端权限，不写入数据
  wzstudy-agent version   打印版本

配置：
  默认读取当前目录的 agent.env（KEY=VALUE），可用 AGENT_CONFIG 指定路径。
  进程环境变量优先于配置文件。必填：WZSTUDY_SERVER_URL、WZSTUDY_AGENT_KEY、
  AGENT_MODEL；模型密钥 AGENT_MODEL_API_KEY 按需填写。
`, version)
}

func newLogger(level string) *slog.Logger {
	parsed := slog.LevelInfo
	switch level {
	case "debug":
		parsed = slog.LevelDebug
	case "warn":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parsed}))
}

func load() (config.Config, *slog.Logger, *serverapi.Client, *loop.Runner, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, nil, nil, nil, err
	}
	cfg.Version = version
	logger := newLogger(cfg.LogLevel)
	client, err := serverapi.New(cfg.ServerURL, cfg.AgentKey, cfg.Version)
	if err != nil {
		return cfg, nil, nil, nil, err
	}
	model, err := provider.NewOpenAI(cfg.ModelBase, cfg.ModelKey)
	if err != nil {
		return cfg, nil, nil, nil, err
	}
	catalog := skill.Load(cfg.SkillsDir)
	logger.Info("skills loaded", "count", len(catalog.Skills()), "dir", cfg.SkillsDir)
	runner := &loop.Runner{
		Provider: model,
		Model:    cfg.Model,
		Skills:   catalog,
		Logger:   logger,
		BuildTools: func(task *serverapi.Task, attempt int, control *tool.FinishControl, stats *serverapi.Stats) *tool.Registry {
			registry := tool.NewRegistry()
			fetcher := collect.NewFetcher(cfg.MaxFetch, cfg.AllowLocal)
			registry.Register(fetcher.Definition(), fetcher.Handle)
			ingestor := collect.NewIngestor(client, task, attempt, stats)
			registry.Register(ingestor.Definition(), ingestor.Handle)
			loader := collect.NewSkillLoader(catalog)
			registry.Register(loader.Definition(), loader.Handle)
			registry.Register(collect.FinishDefinition(), collect.FinishHandler(control))
			return registry
		},
	}
	return cfg, logger, client, runner, nil
}

func run(once bool) int {
	cfg, logger, client, runner, err := load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	w := &worker.Worker{Config: cfg, Client: client, Runner: runner, Logger: logger}
	if once {
		if err := w.Once(ctx); err != nil {
			logger.Error("task run failed", "error", err)
			return 1
		}
		return 0
	}
	if err := w.Run(ctx); err != nil {
		logger.Error("agent stopped", "error", err)
		return 1
	}
	return 0
}

func doctor() int {
	cfg, logger, client, _, err := load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	status, err := client.Status(ctx)
	if err != nil {
		switch {
		case serverapi.IsStatus(err, http.StatusUnauthorized):
			fmt.Fprintln(os.Stderr, "采集端密钥无效或已被轮换：请核对 agent.env 中的 WZSTUDY_AGENT_KEY。")
		case serverapi.IsStatus(err, http.StatusNotFound):
			fmt.Fprintln(os.Stderr, "录入系统未启用采集模块：请在服务端启用 AGENT_ENABLED（默认开启）后重启。")
		default:
			fmt.Fprintf(os.Stderr, "服务端检查失败：%v\n", err)
		}
		return 1
	}
	logger.Info("doctor ok",
		"server", cfg.ServerURL,
		"agent", status.Agent.Name,
		"agent_id", status.Agent.ID,
		"enabled", status.Agent.Enabled,
		"permissions", status.Agent.Permissions,
		"lease_seconds", status.LeaseSeconds,
		"model", cfg.Model,
		"model_base", cfg.ModelBase,
		"skills_dir", cfg.SkillsDir,
		"allow_private_network", cfg.AllowLocal,
	)
	fmt.Println("配置、服务端连通性与采集端身份正常；未调用模型、未写入数据。")
	return 0
}
