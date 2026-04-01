package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/arvindell/glab-overseer/internal/model"
	"github.com/arvindell/glab-overseer/internal/watcher"
)

func Run(ctx context.Context, events chan<- watcher.Event) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	startedAt := time.Now().Add(-24 * time.Minute)
	base := snapshot(startedAt, 0)
	events <- watcher.Event{Snapshot: base}

	step := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			step = (step + 1) % 6
			events <- watcher.Event{Snapshot: snapshot(startedAt, step)}
		}
	}
}

func snapshot(startedAt time.Time, step int) model.Snapshot {
	now := time.Now()
	pipelineStatus := "running"
	if step == 5 {
		pipelineStatus = "success"
	}

	return model.Snapshot{
		Project: "demo/acme-platform",
		Pipeline: model.Pipeline{
			ID:        424242,
			IID:       42,
			Status:    pipelineStatus,
			WebURL:    "https://gitlab.example.com/demo/acme-platform/-/pipelines/424242",
			Source:    "push",
			Ref:       "main",
			CreatedAt: startedAt,
			UpdatedAt: now,
			UserName:  "Alex Doe",
		},
		Stages:    stages(startedAt, step),
		UpdatedAt: now,
		Triggered: step == 0,
		ActionText: func() string {
			if step == 0 {
				return "demo"
			}
			return ""
		}(),
	}
}

func stages(startedAt time.Time, step int) []model.Stage {
	logs := map[string]string{
		"prepare-workspace":    "$ checkout main\n$ restore-cache\nworkspace ready\n",
		"resolve-dependencies": "$ pnpm install --frozen-lockfile\nLockfile is up to date\nPackages restored from cache\n",
		"compile-cli":          "$ go build ./...\ncompiling packages...\nbuild complete\n",
		"bundle-assets":        "$ pnpm build:assets\noptimized assets written to dist/\n",
		"typecheck":            "$ go vet ./...\nno issues found\n",
		"lint":                 "$ golangci-lint run\n0 problems found\n",
		"unit-tests":           "$ go test ./...\nok  ./internal/...\n",
		"e2e-smoke":            "$ ./scripts/smoke.sh\nchecking startup...\nchecking health endpoint...\n",
		"deploy-preview":       "$ deploy --env preview\nreleasing candidate build\n",
		"notify-team":          "$ notify\nmessage sent to release channel\n",
	}

	makeJob := func(id int64, stage, name, status string, duration time.Duration) model.Job {
		started := startedAt.Add(time.Duration(id) * 15 * time.Second)
		finished := started.Add(duration)
		job := model.Job{
			ID:       id,
			Stage:    stage,
			Name:     name,
			Status:   status,
			WebURL:   fmt.Sprintf("https://gitlab.example.com/demo/acme-platform/-/jobs/%d", id),
			Duration: duration,
			Trace:    logs[name],
		}
		job.StartedAt = &started
		if status != "running" && status != "pending" && status != "created" {
			job.FinishedAt = &finished
		}
		return job
	}

	setup := []model.Job{
		makeJob(1, "setup", "prepare-workspace", "success", 45*time.Second),
		makeJob(2, "setup", "resolve-dependencies", "success", 2*time.Minute+14*time.Second),
	}

	buildStatus := []string{"running", "running", "success", "success", "success", "success"}[step]
	verifyStatus := []string{"pending", "running", "running", "success", "success", "success"}[step]
	promoteStatus := []string{"created", "created", "pending", "running", "success", "success"}[step]
	postStatus := []string{"created", "created", "created", "pending", "running", "success"}[step]

	build := []model.Job{
		makeJob(3, "build", "compile-cli", buildStatus, 3*time.Minute+6*time.Second),
		makeJob(4, "build", "bundle-assets", func() string {
			if step < 2 {
				return "success"
			}
			return "success"
		}(), 1*time.Minute+12*time.Second),
	}

	verify := []model.Job{
		makeJob(5, "verify", "typecheck", verifyStatus, 58*time.Second),
		makeJob(6, "verify", "lint", verifyStatus, 43*time.Second),
		makeJob(7, "verify", "unit-tests", verifyStatus, 1*time.Minute+27*time.Second),
		makeJob(8, "verify", "e2e-smoke", verifyStatus, 2*time.Minute+9*time.Second),
	}

	promote := []model.Job{
		makeJob(9, "promote", "deploy-preview", promoteStatus, 1*time.Minute+8*time.Second),
	}

	post := []model.Job{
		makeJob(10, "post", "notify-team", postStatus, 12*time.Second),
	}

	return []model.Stage{
		{Name: "setup", Jobs: setup},
		{Name: "build", Jobs: build},
		{Name: "verify", Jobs: verify},
		{Name: "promote", Jobs: promote},
		{Name: "post", Jobs: post},
	}
}
