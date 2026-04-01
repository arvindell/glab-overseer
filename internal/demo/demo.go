package demo

import (
	"context"
	"fmt"
	"strings"
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
			step = (step + 1) % 8
			events <- watcher.Event{Snapshot: snapshot(startedAt, step)}
		}
	}
}

func snapshot(startedAt time.Time, step int) model.Snapshot {
	now := time.Now()
	pipelineStatus := "running"
	if step == 7 {
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
	logBlock := func(lines ...string) string {
		return strings.Join(lines, "\n") + "\n"
	}

	logs := map[string]string{
		"prepare-workspace": logBlock(
			"$ git fetch origin main --depth=20",
			"$ git checkout --force 4c9af0f",
			"HEAD is now at 4c9af0f chore: improve release summary",
			"$ mkdir -p .cache/build .cache/assets",
			"Workspace prepared",
		),
		"restore-cache": logBlock(
			"$ cachectl restore build-cache-linux-arm64",
			"Cache hit: build-cache-linux-arm64",
			"Restored 248 MB in 1.8s",
		),
		"resolve-dependencies": logBlock(
			"$ pnpm install --frozen-lockfile",
			"Lockfile is up to date, resolution step is skipped",
			"Progress: resolved 0, reused 913, downloaded 0, added 0",
			"Packages: +0",
			"Done in 2.4s",
		),
		"compile-cli": logBlock(
			"$ go build ./...",
			"building cmd/glab-overseer",
			"building internal/demo",
			"building internal/ui",
			"linking glab-overseer",
			"Build complete",
		),
		"bundle-assets": logBlock(
			"$ pnpm build:assets",
			"Generating static styles",
			"Compressing SVG sprite sheet",
			"Writing dist/assets-manifest.json",
			"Assets optimized successfully",
		),
		"dockerize-api": logBlock(
			"$ docker build -f Dockerfile.api .",
			"#1 [internal] load build definition from Dockerfile.api",
			"#7 exporting layers",
			"#7 naming to registry.example.com/acme/api:preview-424242",
			"Pushed registry.example.com/acme/api:preview-424242",
		),
		"dockerize-worker": logBlock(
			"$ docker build -f Dockerfile.worker .",
			"#1 [internal] load build definition from Dockerfile.worker",
			"#8 exporting layers",
			"#8 naming to registry.example.com/acme/worker:preview-424242",
			"Pushed registry.example.com/acme/worker:preview-424242",
		),
		"typecheck": logBlock(
			"$ go vet ./...",
			"vet: internal/watcher ok",
			"vet: internal/ui ok",
			"No issues found",
		),
		"lint": logBlock(
			"$ golangci-lint run",
			"Checked 42 packages",
			"0 issues.",
		),
		"unit-tests": logBlock(
			"$ go test ./... -count=1",
			"ok  github.com/acme/glab-overseer/internal/state  0.128s",
			"ok  github.com/acme/glab-overseer/internal/gitlab 0.271s",
			"ok  github.com/acme/glab-overseer/internal/watcher 0.342s",
		),
		"integration-tests": logBlock(
			"$ go test ./test/integration -count=1",
			"spinning up ephemeral gitlab fixture",
			"seeded demo pipeline and jobs",
			"integration suite passed",
		),
		"e2e-smoke": logBlock(
			"$ ./scripts/smoke.sh",
			"checking startup... ok",
			"checking /health endpoint... ok",
			"checking demo mode rendering... ok",
			"smoke suite complete",
		),
		"package-release": logBlock(
			"$ goreleaser release --snapshot --clean",
			"building darwin_arm64",
			"building linux_amd64",
			"archive created glab-overseer_v0.9.0_linux_arm64.tar.gz",
			"snapshot packaging complete",
		),
		"deploy-preview": logBlock(
			"$ deploy preview --env staging",
			"Uploading release candidate",
			"Warming edge cache",
			"Preview available at https://preview-424242.example.net",
		),
		"run-migrations": logBlock(
			"$ migrate --database staging up",
			"Applying 2 migrations",
			"schema_migrations updated",
			"Database is up to date",
		),
		"mark-release": logBlock(
			"$ releasectl mark v0.9.0-preview.4",
			"Preview build promoted",
			"Release metadata updated",
		),
		"notify-team": logBlock(
			"$ notify --channel release-preview",
			"message queued",
			"delivery confirmed",
		),
		"prune-cache": logBlock(
			"$ cachectl prune --max-age 14d",
			"Removed 17 stale entries",
			"Freed 3.4 GB",
		),
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
		makeJob(2, "setup", "restore-cache", "success", 27*time.Second),
		makeJob(3, "setup", "resolve-dependencies", "success", 2*time.Minute+14*time.Second),
	}

	buildStatus := []string{"running", "running", "running", "success", "success", "success", "success", "success"}[step]
	verifyStatus := []string{"pending", "pending", "running", "running", "success", "success", "success", "success"}[step]
	packageStatus := []string{"created", "created", "pending", "running", "running", "success", "success", "success"}[step]
	promoteStatus := []string{"created", "created", "created", "pending", "running", "running", "success", "success"}[step]
	postStatus := []string{"created", "created", "created", "created", "pending", "running", "running", "success"}[step]

	build := []model.Job{
		makeJob(4, "build", "compile-cli", buildStatus, 3*time.Minute+6*time.Second),
		makeJob(5, "build", "bundle-assets", "success", 1*time.Minute+12*time.Second),
		makeJob(6, "build", "dockerize-api", buildStatus, 4*time.Minute+8*time.Second),
		makeJob(7, "build", "dockerize-worker", buildStatus, 3*time.Minute+42*time.Second),
	}

	verify := []model.Job{
		makeJob(8, "verify", "typecheck", verifyStatus, 58*time.Second),
		makeJob(9, "verify", "lint", verifyStatus, 43*time.Second),
		makeJob(10, "verify", "unit-tests", verifyStatus, 1*time.Minute+27*time.Second),
		makeJob(11, "verify", "integration-tests", verifyStatus, 2*time.Minute+2*time.Second),
		makeJob(12, "verify", "e2e-smoke", verifyStatus, 2*time.Minute+9*time.Second),
		makeJob(13, "verify", "package-release", packageStatus, 1*time.Minute+36*time.Second),
	}

	promote := []model.Job{
		makeJob(14, "promote", "deploy-preview", promoteStatus, 1*time.Minute+8*time.Second),
		makeJob(15, "promote", "run-migrations", promoteStatus, 41*time.Second),
		makeJob(16, "promote", "mark-release", promoteStatus, 18*time.Second),
	}

	post := []model.Job{
		makeJob(17, "post", "notify-team", postStatus, 12*time.Second),
		makeJob(18, "post", "prune-cache", postStatus, 21*time.Second),
	}

	return []model.Stage{
		{Name: "setup", Jobs: setup},
		{Name: "build", Jobs: build},
		{Name: "verify", Jobs: verify},
		{Name: "promote", Jobs: promote},
		{Name: "post", Jobs: post},
	}
}
