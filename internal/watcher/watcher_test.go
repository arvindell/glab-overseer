package watcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arvindell/glab-overseer/internal/config"
	"github.com/arvindell/glab-overseer/internal/gitlab"
	"github.com/arvindell/glab-overseer/internal/model"
	"github.com/arvindell/glab-overseer/internal/state"
)

func TestRefreshTracesIncludesChildrenAndSkipsTriggerLogs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/1/pipelines/10":
			fmt.Fprint(w, `{"id":10,"status":"running"}`)
		case "/api/v4/projects/1/pipelines/10/jobs":
			fmt.Fprint(w, `[]`)
		case "/api/v4/projects/1/pipelines/10/bridges":
			fmt.Fprint(w, `[{"id":1,"name":"rollout","stage":"trigger","status":"running","downstream_pipeline":{"id":20,"project_id":2}}]`)
		case "/api/v4/projects/2/pipelines/20/jobs":
			fmt.Fprint(w, `[{"id":2,"name":"compile","stage":"build","status":"running"}]`)
		case "/api/v4/projects/2/pipelines/20/bridges":
			fmt.Fprint(w, `[]`)
		case "/api/v4/projects/2/jobs/2/trace":
			fmt.Fprint(w, "child job output")
		default:
			t.Errorf("unexpected request %s; trigger logs must not be fetched", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	w := New(gitlab.NewClient(server.URL, "", time.Second, 0), nil, nil, config.Config{Project: "root/project"}, nil)
	w.projectID, w.currentID = 1, 10
	w.traceJobID = 2
	w.selectedJobs = []model.Job{
		{ID: 1, IsBridge: true, Status: "running", DownstreamPipelineID: 20},
		{ID: 2, ProjectID: 2, PipelineID: 20, Name: "compile", Stage: "build", Status: "running"},
	}
	events := make(chan Event, 1)
	if err := w.refreshTraces(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	event := <-events
	if len(event.Snapshot.Stages) != 2 {
		t.Fatalf("expected trigger and child stages, got %d", len(event.Snapshot.Stages))
	}
	job := event.Snapshot.Stages[1].Jobs[0]
	if job.ProjectID != 2 || job.Trace != "child job output" || job.TraceSize != int64(len(job.Trace)) {
		t.Fatalf("unexpected child trace: %+v", job)
	}
}

func TestRefreshCachesCompletedPipelinesAndDetectsRetry(t *testing.T) {
	requests := map[string]int{}
	status := "success"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch {
		case r.URL.Path == "/api/v4/projects/1/pipelines":
			if r.URL.Query().Get("ref") != "main" {
				t.Error("branch filter was ignored")
			}
			fmt.Fprint(w, "[")
			for id := 8; id > 0; id-- {
				if id != 8 {
					fmt.Fprint(w, ",")
				}
				pipelineStatus := "success"
				if id == 8 {
					pipelineStatus = status
				}
				fmt.Fprintf(w, `{"id":%d,"status":%q,"sha":"same"}`, id, pipelineStatus)
			}
			fmt.Fprint(w, "]")
		case strings.HasSuffix(r.URL.Path, "/jobs"):
			fmt.Fprintf(w, `[{"id":80,"stage":"build","status":%q}]`, status)
		case strings.HasSuffix(r.URL.Path, "/bridges"):
			fmt.Fprint(w, "[]")
		case strings.Contains(r.URL.Path, "/repository/commits/"):
			fmt.Fprint(w, `{"title":"Cached commit"}`)
		default:
			var id int
			if _, err := fmt.Sscanf(r.URL.Path, "/api/v4/projects/1/pipelines/%d", &id); err != nil {
				t.Fatal(err)
			}
			pipelineStatus := "success"
			if id == 8 {
				pipelineStatus = status
			}
			fmt.Fprintf(w, `{"id":%d,"status":%q,"sha":"same"}`, id, pipelineStatus)
		}
	}))
	defer server.Close()
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	w := New(gitlab.NewClient(server.URL, "", time.Second, 0), store, nil, config.Config{Project: "root/project", Ref: "main"}, nil)
	w.projectID = 1
	events := make(chan Event, 32)
	for range 8 {
		if err := w.refresh(context.Background(), events); err != nil {
			t.Fatal(err)
		}
	}
	if requests["/api/v4/projects/1/repository/commits/same"] != 1 {
		t.Fatal("commit title was fetched repeatedly")
	}
	clear(requests)
	if err := w.refresh(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests["/api/v4/projects/1/pipelines"] != 1 {
		t.Fatalf("settled history must only require the recent-pipeline list: %v", requests)
	}
	status = "running"
	if err := w.refresh(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	if w.selectedJobs[0].Status != "running" || requests["/api/v4/projects/1/pipelines/8/jobs"] != 1 {
		t.Fatal("retried pipeline did not invalidate its completed cache")
	}
}

func TestOnlyViewedJobTraceIsFetchedAndCompletedTraceIsCached(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v4/projects/2/jobs/2/trace" {
			t.Errorf("unviewed job requested: %s", r.URL.Path)
		}
		fmt.Fprint(w, "finished log")
	}))
	defer server.Close()
	w := New(gitlab.NewClient(server.URL, "", time.Second, 0), nil, nil, config.Config{}, nil)
	w.currentID, w.traceJobID = 10, 2
	w.selectedJobs = []model.Job{
		{ID: 2, ProjectID: 2, Stage: "build", Status: "success"},
		{ID: 3, ProjectID: 2, Stage: "build", Status: "running"},
	}
	events := make(chan Event, 2)
	for range 2 {
		if err := w.refreshTraces(context.Background(), events); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 || w.cachedTrace(2) != "finished log" {
		t.Fatalf("expected one final trace fetch, got %d", requests)
	}
}

func TestStartupRateLimitWaitsForRecoveryInsteadOfStoppingWatcher(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	w := New(gitlab.NewClient(server.URL, "", time.Second, 0), nil, nil, config.Config{Project: "root/project"}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 4)
	done := make(chan struct{})
	go func() { w.Run(ctx, events); close(done) }()
	select {
	case event := <-events:
		if event.Err == nil {
			t.Fatal("missing startup rate-limit event")
		}
	case <-time.After(time.Second):
		t.Fatal("watcher did not report startup throttling")
	}
	select {
	case <-done:
		t.Fatal("watcher stopped instead of waiting for the quota reset")
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cooldown prevented shutdown")
	}
	if requests != 1 {
		t.Fatalf("requests continued during cooldown: %d", requests)
	}
}
