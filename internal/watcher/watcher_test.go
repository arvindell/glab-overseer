package watcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arvindell/glab-overseer/internal/config"
	"github.com/arvindell/glab-overseer/internal/gitlab"
	"github.com/arvindell/glab-overseer/internal/model"
	"github.com/arvindell/glab-overseer/internal/state"
)

func TestRefreshPublishesPipelinesBeforeDetailsFinish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	detailsStarted := make(chan struct{})
	releaseDetails := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/1/pipelines":
			fmt.Fprint(w, `[{"id":10,"status":"success","ref":"main"}]`)
		case "/api/v4/projects/1/pipelines/10":
			close(detailsStarted)
			select {
			case <-releaseDetails:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `{"id":10,"status":"success","ref":"main"}`)
		case "/api/v4/projects/1/pipelines/10/jobs":
			fmt.Fprint(w, `[{"id":100,"stage":"build","status":"success"}]`)
		case "/api/v4/projects/1/pipelines/10/bridges":
			fmt.Fprint(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	defer cancel()
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	w := New(gitlab.NewClient(server.URL, "", time.Second, 0), store, nil, config.Config{Project: "root/project"}, nil)
	w.projectID = 1
	events := make(chan Event, 4)
	done := make(chan error, 1)
	go func() { done <- w.refresh(ctx, events) }()
	<-detailsStarted
	select {
	case event := <-events:
		if len(event.Snapshot.Pipelines) != 1 || event.Snapshot.Pipelines[0].Pipeline.ID != 10 || event.Snapshot.Pipeline.ID != 10 || !event.Snapshot.Loading || !event.Snapshot.Triggered {
			t.Fatalf("expected current pipeline identity before loading details: %+v", event.Snapshot)
		}
	default:
		t.Fatal("pipeline list was blocked by loading job details")
	}
	close(releaseDetails)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if event := <-events; event.Snapshot.Pipeline.ID != 10 || len(event.Snapshot.Stages) != 1 {
		t.Fatal("finished job details were not published")
	}
}

func TestRunDoesNotRestoreAnOldPipelineFromAQueuedUICommand(t *testing.T) {
	var oldPipelineRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/root/project":
			fmt.Fprint(w, `{"id":1}`)
		case "/api/v4/projects/1/pipelines":
			fmt.Fprint(w, `[{"id":20,"status":"running"},{"id":10,"status":"success"}]`)
		case "/api/v4/projects/1/pipelines/20":
			fmt.Fprint(w, `{"id":20,"status":"running"}`)
		case "/api/v4/projects/1/pipelines/20/jobs":
			fmt.Fprint(w, `[{"id":200,"stage":"build","status":"running"}]`)
		case "/api/v4/projects/1/pipelines/20/bridges":
			fmt.Fprint(w, `[]`)
		case "/api/v4/projects/1/jobs/200/trace":
			fmt.Fprint(w, "current pipeline log")
		case "/api/v4/projects/1/pipelines/10":
			oldPipelineRequests.Add(1)
			fmt.Fprint(w, `{"id":10,"status":"success"}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	commands := make(chan Command, 4)
	w := New(gitlab.NewClient(server.URL, "", time.Second, 0), store, nil, config.Config{
		Project: "root/project", PollInterval: time.Hour, TraceInterval: time.Hour,
	}, commands)
	w.currentID = 10
	w.pipelines[10] = cachedPipeline{pipeline: model.Pipeline{ID: 10, Status: "success"}}
	// These arrive while a recursive refresh detects pipeline 20. A valid trace
	// command must survive even if stale selections are queued before and after it.
	jobID := int64(200)
	commands <- Command{ExpectedPipelineID: 10, SelectPipelineID: 10}
	commands <- Command{ExpectedPipelineID: 20, SelectJobID: &jobID}
	commands <- Command{ExpectedPipelineID: 10, SelectPipelineID: 10}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 16)
	done := make(chan struct{})
	go func() { w.Run(ctx, events); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Err != nil || event.Snapshot.Pipeline.ID != 20 {
				t.Fatalf("newest pipeline was replaced: %+v", event)
			}
			if len(event.Snapshot.Stages) > 0 && event.Snapshot.Stages[0].Jobs[0].Trace == "current pipeline log" {
				if oldPipelineRequests.Load() != 0 {
					t.Fatal("queued old selection fetched an obsolete pipeline")
				}
				return
			}
		case <-deadline:
			t.Fatal("valid current-view command was lost behind stale selections")
		}
	}
}

func TestRefreshPublishesNewestPipelineEvenWhenItsJobsFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/1/pipelines":
			fmt.Fprint(w, `[{"id":20,"status":"running"}]`)
		case "/api/v4/projects/1/pipelines/20":
			fmt.Fprint(w, `{"id":20,"status":"running"}`)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	w := New(gitlab.NewClient(server.URL, "", time.Second, 0), store, nil, config.Config{Project: "root/project"}, nil)
	w.projectID, w.currentID = 1, 10
	w.selectedPipeline = model.Pipeline{ID: 10}
	w.selectedJobs = []model.Job{{ID: 100}}
	events := make(chan Event, 4)
	for attempt := range 2 {
		if err := w.refresh(context.Background(), events); err == nil {
			t.Fatal("expected job loading to fail")
		}
		snapshot := (<-events).Snapshot
		if snapshot.Pipeline.ID != 20 || !snapshot.Loading || len(snapshot.Stages) != 0 || snapshot.Triggered != (attempt == 0) {
			t.Fatalf("failed hydration left an old pipeline visible or lost the trigger: %+v", snapshot)
		}
	}
}

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
	if len(event.Snapshot.ParentJobs) != 1 || len(event.Snapshot.Stages) != 1 {
		t.Fatalf("expected compact parent trigger and one workload stage: %+v", event.Snapshot)
	}
	job := event.Snapshot.Stages[0].Jobs[0]
	if job.ProjectID != 2 || job.Trace != "child job output" || job.TraceSize != int64(len(job.Trace)) {
		t.Fatalf("unexpected child trace: %+v", job)
	}
}

func TestSnapshotsSeparateParentJobsWithoutLosingWorkloadStages(t *testing.T) {
	w := New(nil, nil, nil, config.Config{}, nil)
	w.currentID = 10
	w.selectedJobs = []model.Job{
		{ID: 1, PipelineID: 10, Name: "bootstrap", Stage: ".pre", IsParent: true, Status: "success"},
		{ID: 2, PipelineID: 10, Name: "orchestrate", Stage: "orchestrate", IsParent: true, IsBridge: true, Status: "running"},
		{ID: 3, PipelineID: 20, Name: "select", Stage: "select", IsParent: true, Status: "success", Trace: "selected deployments"},
		{ID: 4, PipelineID: 20, Name: "run-pipeline", Stage: "rollout", IsParent: true, IsBridge: true, Status: "running"},
		{ID: 5, PipelineID: 30, Name: "promote-image", Stage: "promote", Status: "success"},
		{ID: 6, PipelineID: 30, Name: "deploy-api", Stage: "deploy", Status: "created"},
	}
	w.pipelines[10] = cachedPipeline{pipeline: model.Pipeline{ID: 10}, jobs: w.selectedJobs}
	w.updateOverview([]model.Pipeline{{ID: 10}})
	events := make(chan Event, 1)
	w.emitSnapshot(events, false)
	snapshot := (<-events).Snapshot
	if len(snapshot.ParentJobs) != 4 || len(snapshot.Stages) != 2 || snapshot.Stages[0].Name != "promote" || snapshot.Stages[1].Name != "deploy" {
		t.Fatalf("parent jobs displaced promotion/deployment stages: %+v", snapshot)
	}
	if len(snapshot.Pipelines[0].ParentJobs) != 4 || len(snapshot.Pipelines[0].Stages) != 2 {
		t.Fatal("overview must use the same parent/workload separation")
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

func TestStartupRecoversAfterTemporaryProjectResolutionFailure(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/root/project":
			if attempts.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fmt.Fprint(w, `{"id":1}`)
		case "/api/v4/projects/1/pipelines":
			fmt.Fprint(w, `[{"id":10,"status":"success","ref":"main"}]`)
		case "/api/v4/projects/1/pipelines/10":
			fmt.Fprint(w, `{"id":10,"status":"success","ref":"main"}`)
		case "/api/v4/projects/1/pipelines/10/jobs", "/api/v4/projects/1/pipelines/10/bridges":
			fmt.Fprint(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	w := New(gitlab.NewClient(server.URL, "", time.Second, 0), store, nil, config.Config{
		Project: "root/project", PollInterval: 10 * time.Millisecond, TraceInterval: time.Hour,
	}, nil)
	events := make(chan Event, 8)
	done := make(chan struct{})
	go func() { w.Run(ctx, events); close(done) }()
	if event := <-events; event.Err == nil {
		t.Fatal("startup failure was not reported")
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Snapshot.Pipeline.ID == 10 {
				if attempts.Load() != 2 {
					t.Fatal("project resolution did not recover on the next attempt")
				}
				cancel()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("watcher did not stop after cancellation")
				}
				return
			}
		case <-done:
			t.Fatal("temporary startup failure stopped the watcher")
		case <-deadline:
			t.Fatal("watcher did not recover after the startup failure")
		}
	}
}
