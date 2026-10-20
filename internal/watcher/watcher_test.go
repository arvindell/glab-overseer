package watcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/arvindell/glab-overseer/internal/config"
	"github.com/arvindell/glab-overseer/internal/gitlab"
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
	w := New(gitlab.NewClient(server.URL, "", time.Second), nil, nil, config.Config{Project: "root/project"}, nil)
	w.projectID, w.currentID = 1, 10
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
