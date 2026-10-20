package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/arvindell/glab-overseer/internal/model"
)

func TestPipelineJobsIncludesNestedChildrenAndPaginatedJobsAndBridges(t *testing.T) {
	responses := map[string]string{
		"/api/v4/projects/1/pipelines/10/jobs?page=1":    `[{"id":1,"name":"root-job","stage":"setup"}]`,
		"/api/v4/projects/1/pipelines/10/jobs?page=2":    `[{"id":2,"name":"root-job-2","stage":"setup"}]`,
		"/api/v4/projects/1/pipelines/10/bridges?page=1": `[{"id":3,"name":"rollout","stage":"trigger","status":"running","downstream_pipeline":{"id":20}}]`,
		"/api/v4/projects/1/pipelines/10/bridges?page=2": `[{"id":4,"name":"pending-child","stage":"trigger","status":"pending","downstream_pipeline":null}]`,
		"/api/v4/projects/1/pipelines/20/jobs?page=1":    `[{"id":5,"name":"select","stage":"select"}]`,
		"/api/v4/projects/1/pipelines/20/bridges?page=1": `[{"id":6,"name":"deploy","stage":"trigger","downstream_pipeline":{"id":30,"project_id":2}}]`,
		"/api/v4/projects/2/pipelines/30/jobs?page=1":    `[{"id":7,"name":"build-backend","stage":"build","status":"running"}]`,
		"/api/v4/projects/2/pipelines/30/bridges?page=1": `[]`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
			t.Error("missing authentication header")
		}
		if r.URL.Query().Get("per_page") != "100" {
			t.Error("expected per_page=100")
		}
		key := r.URL.Path + "?page=" + r.URL.Query().Get("page")
		body, ok := responses[key]
		if !ok {
			t.Errorf("unexpected request %s", key)
			http.NotFound(w, r)
			return
		}
		if key == "/api/v4/projects/1/pipelines/10/jobs?page=1" || key == "/api/v4/projects/1/pipelines/10/bridges?page=1" {
			w.Header().Set("X-Next-Page", "2")
		}
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	jobs, err := NewClient(server.URL, "test-token", time.Second).PipelineJobs(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 7 {
		t.Fatalf("expected all 7 jobs and bridges, got %d", len(jobs))
	}
	byID := make(map[int64]model.Job)
	for _, job := range jobs {
		byID[job.ID] = job
	}
	if job := byID[7]; job.ProjectID != 2 || job.PipelineID != 30 || job.PipelinePath != "rollout / deploy" || job.IsBridge {
		t.Fatalf("unexpected nested job provenance: %+v", job)
	}
	if job := byID[3]; !job.IsBridge || job.DownstreamPipelineID != 20 || job.ProjectID != 1 || job.PipelineID != 10 {
		t.Fatalf("unexpected root bridge: %+v", job)
	}
	if job := byID[4]; !job.IsBridge || job.DownstreamPipelineID != 0 {
		t.Fatalf("pending trigger must remain visible before its child exists: %+v", job)
	}
}

func TestPipelineJobsVisitsSharedOrCyclicChildrenOnce(t *testing.T) {
	requests := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch r.URL.Path {
		case "/api/v4/projects/1/pipelines/10/jobs", "/api/v4/projects/1/pipelines/20/jobs":
			fmt.Fprint(w, `[]`)
		case "/api/v4/projects/1/pipelines/10/bridges":
			fmt.Fprint(w, `[{"id":1,"name":"first","downstream_pipeline":{"id":20}},{"id":2,"name":"second","downstream_pipeline":{"id":20}}]`)
		case "/api/v4/projects/1/pipelines/20/bridges":
			fmt.Fprint(w, `[{"id":3,"name":"back","downstream_pipeline":{"id":10}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	jobs, err := NewClient(server.URL, "", time.Second).PipelineJobs(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected three unique bridges, got %d", len(jobs))
	}
	for endpoint, count := range requests {
		if count != 1 {
			t.Fatalf("endpoint %s fetched %d times", endpoint, count)
		}
	}
}

func TestPipelineJobsReportsInaccessibleChildInsteadOfHidingJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/1/pipelines/10/jobs":
			fmt.Fprint(w, `[]`)
		case "/api/v4/projects/1/pipelines/10/bridges":
			fmt.Fprint(w, `[{"id":1,"name":"rollout","downstream_pipeline":{"id":20}}]`)
		default:
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "", time.Second).PipelineJobs(context.Background(), 1, 10)
	if err == nil || !strings.Contains(err.Error(), "pipeline 20") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected contextual child error, got %v", err)
	}
}

func TestPipelineJobsSupportsMoreThanOneHundredJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/bridges") {
			fmt.Fprint(w, `[]`)
			return
		}
		start, end := 1, 100
		if r.URL.Query().Get("page") == "2" {
			start, end = 101, 105
		} else {
			w.Header().Set("X-Next-Page", "2")
		}
		var jobs []jobResponse
		for id := start; id <= end; id++ {
			jobs = append(jobs, jobResponse{ID: int64(id), Name: fmt.Sprintf("job-%d", id), Stage: "verify"})
		}
		json.NewEncoder(w).Encode(jobs)
	}))
	defer server.Close()
	jobs, err := NewClient(server.URL, "", time.Second).PipelineJobs(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 105 || jobs[104].ID != 105 {
		t.Fatalf("expected every paginated job, got %d", len(jobs))
	}
}

func TestGroupJobsByStageUsesEarliestJobOrder(t *testing.T) {
	jobs := []model.Job{
		{ID: 30, Stage: "verify", Name: "lint"},
		{ID: 10, Stage: "setup", Name: "install"},
		{ID: 20, Stage: "build", Name: "compile"},
		{ID: 31, Stage: "verify", Name: "test"},
	}

	stages := GroupJobsByStage(jobs)
	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}

	want := []string{"setup", "build", "verify"}
	for i, stage := range stages {
		if stage.Name != want[i] {
			t.Fatalf("expected stage %q at index %d, got %q", want[i], i, stage.Name)
		}
	}
}

func TestSummarizeStagesPrefersFailedThenRunning(t *testing.T) {
	jobs := []model.Job{
		{ID: 1, Stage: "setup", Status: "success"},
		{ID: 2, Stage: "build", Status: "running"},
		{ID: 3, Stage: "build", Status: "success"},
		{ID: 4, Stage: "verify", Status: "failed"},
	}

	summaries := SummarizeStages(jobs)
	if len(summaries) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(summaries))
	}

	if summaries[1].Status != "running" {
		t.Fatalf("expected build summary to be running, got %s", summaries[1].Status)
	}
	if summaries[2].Status != "failed" {
		t.Fatalf("expected verify summary to be failed, got %s", summaries[2].Status)
	}
}
