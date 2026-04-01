package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/arvindell/glab-overseer/internal/actions"
	"github.com/arvindell/glab-overseer/internal/config"
	"github.com/arvindell/glab-overseer/internal/demo"
	"github.com/arvindell/glab-overseer/internal/gitlab"
	"github.com/arvindell/glab-overseer/internal/state"
	"github.com/arvindell/glab-overseer/internal/ui"
	"github.com/arvindell/glab-overseer/internal/watcher"
)

var version = "dev"

func main() {
	_ = godotenv.Load()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dispatcher := actions.NewDispatcher(cfg.Action, 4)
	defer dispatcher.Close()

	events := make(chan watcher.Event, 64)
	commands := make(chan watcher.Command, 16)
	if cfg.Demo {
		go demo.Run(ctx, events)
		if err := ui.Run(ctx, events, commands, dispatcher); err != nil {
			log.Fatal(err)
		}
		return
	}

	store, err := state.NewStore(cfg.StateFile)
	if err != nil {
		log.Fatal(err)
	}

	client := gitlab.NewClient(cfg.Host, cfg.Token, 20*time.Second)
	w := watcher.New(client, store, dispatcher, cfg, commands)
	go w.Run(ctx, events)

	if err := ui.Run(ctx, events, commands, dispatcher); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() (config.Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.Config{}, fmt.Errorf("get home dir: %w", err)
	}

	defaultStateFile := filepath.Join(home, ".config", "glab-overseer", "state.json")

	project := flag.String("project", os.Getenv("GITLAB_PROJECT"), "GitLab project path, e.g. group/project")
	host := flag.String("host", config.EnvOrDefault("GITLAB_HOST", "https://gitlab.com"), "GitLab host URL")
	token := flag.String("token", os.Getenv("GITLAB_TOKEN"), "GitLab personal access token")
	demoMode := flag.Bool("demo", false, "Run with built-in mock pipeline data for screenshots and demos")
	ref := flag.String("ref", os.Getenv("GITLAB_REF"), "Optional branch/ref filter")
	interval := flag.Duration("interval", config.DurationEnvOrDefault("OVERSEER_POLL_INTERVAL", 15*time.Second), "Pipeline poll interval")
	traceInterval := flag.Duration("trace-interval", config.DurationEnvOrDefault("OVERSEER_TRACE_INTERVAL", 3*time.Second), "Job trace poll interval")
	action := flag.String("action", config.EnvOrDefault("OVERSEER_ACTION", string(actions.ActionLog)), "Action on new pipeline: none, log, open")
	stateFile := flag.String("state-file", config.EnvOrDefault("OVERSEER_STATE_FILE", defaultStateFile), "Path to state file")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	if !*demoMode && *project == "" {
		return config.Config{}, fmt.Errorf("missing project: set --project or GITLAB_PROJECT")
	}
	if !*demoMode && *token == "" {
		return config.Config{}, fmt.Errorf("missing token: set --token or GITLAB_TOKEN")
	}

	parsedAction, err := actions.ParseAction(*action)
	if err != nil {
		return config.Config{}, err
	}

	return config.Config{
		Demo:          *demoMode,
		Project:       *project,
		Host:          *host,
		Token:         *token,
		Ref:           *ref,
		PollInterval:  *interval,
		TraceInterval: *traceInterval,
		Action:        parsedAction,
		StateFile:     *stateFile,
	}, nil
}
