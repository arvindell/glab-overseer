package gitlab

import (
	"testing"

	"github.com/arvindell/glab-overseer/internal/model"
)

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
