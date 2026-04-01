package ui

import (
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/arvindell/glab-overseer/internal/model"
	"github.com/arvindell/glab-overseer/internal/watcher"
)

func TestTriggeredPipelineSwitchesToInspect(t *testing.T) {
	m := newTestModel()
	snapshot := makeSnapshot(101, "running", true)

	updated, _ := m.Update(eventMsg{Snapshot: snapshot})
	ui := updated.(modelUI)

	if ui.viewMode != viewModeInspect {
		t.Fatalf("expected inspect mode, got %s", ui.viewMode)
	}
	if ui.selectedPipelineID != 101 {
		t.Fatalf("expected selected pipeline 101, got %d", ui.selectedPipelineID)
	}
	if ui.autoInspectPipelineID != 101 {
		t.Fatalf("expected auto inspect pipeline 101, got %d", ui.autoInspectPipelineID)
	}
}

func TestAutoInspectedPipelineReturnsToOverviewOnSuccess(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(eventMsg{Snapshot: makeSnapshot(101, "running", true)})
	ui := updated.(modelUI)

	updated, _ = ui.Update(eventMsg{Snapshot: makeSnapshot(101, "success", false)})
	ui = updated.(modelUI)

	if ui.viewMode != viewModeOverview {
		t.Fatalf("expected overview mode after success, got %s", ui.viewMode)
	}
	if ui.autoInspectPipelineID != 0 {
		t.Fatalf("expected auto inspect pipeline to clear, got %d", ui.autoInspectPipelineID)
	}
}

func TestAutoInspectedPipelineStaysInspectOnFailure(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(eventMsg{Snapshot: makeSnapshot(101, "running", true)})
	ui := updated.(modelUI)

	updated, _ = ui.Update(eventMsg{Snapshot: makeSnapshot(101, "failed", false)})
	ui = updated.(modelUI)

	if ui.viewMode != viewModeInspect {
		t.Fatalf("expected inspect mode on failure, got %s", ui.viewMode)
	}
	if !ui.isInspectFailed() {
		t.Fatal("expected failed inspect state")
	}
}

func TestManualInspectDoesNotAutoReturnOnSuccess(t *testing.T) {
	m := newTestModel()
	m.snapshot = makeSnapshot(201, "running", false)
	m.snapshot.Pipelines = []model.PipelineSummary{{Pipeline: model.Pipeline{ID: 201, Ref: "main"}}}
	m.selectedPipelineID = 201
	m.viewMode = viewModeOverview

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	ui := updated.(modelUI)
	ui.snapshot = makeSnapshot(201, "success", false)
	ui.snapshot.SelectedPipelineID = 201

	updated, _ = ui.Update(eventMsg{Snapshot: ui.snapshot})
	ui = updated.(modelUI)

	if ui.viewMode != viewModeInspect {
		t.Fatalf("expected manual inspect to stay open on success, got %s", ui.viewMode)
	}
}

func TestOverviewEnterSendsSelectCommand(t *testing.T) {
	commands := make(chan watcher.Command, 1)
	m := newTestModel()
	m.commands = commands
	m.snapshot = makeSnapshot(301, "running", false)
	m.snapshot.Pipelines = []model.PipelineSummary{
		{Pipeline: model.Pipeline{ID: 301, Ref: "main"}},
		{Pipeline: model.Pipeline{ID: 302, Ref: "development"}},
	}
	m.overviewIndex = 1
	m.selectedPipelineID = 302

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	ui := updated.(modelUI)

	if ui.viewMode != viewModeInspect {
		t.Fatalf("expected inspect mode, got %s", ui.viewMode)
	}

	select {
	case cmd := <-commands:
		if cmd.SelectPipelineID != 302 {
			t.Fatalf("expected select command for 302, got %d", cmd.SelectPipelineID)
		}
	default:
		t.Fatal("expected select pipeline command")
	}
}

func newTestModel() modelUI {
	return modelUI{
		commands:    make(chan watcher.Command, 4),
		focusMode:   focusModeDefault,
		viewMode:    viewModeOverview,
		now:         time.Unix(1711970000, 0),
		width:       120,
		height:      40,
		logViewport: viewportForTests(),
	}
}

func viewportForTests() viewport.Model {
	vp := viewport.New(80, 10)
	return vp
}

func makeSnapshot(id int64, status string, triggered bool) model.Snapshot {
	pipeline := model.Pipeline{
		ID:        id,
		Ref:       "main",
		Status:    status,
		UserName:  "Alex",
		CreatedAt: time.Unix(1711970000, 0),
	}
	return model.Snapshot{
		Project:            "quentli/quentli",
		Pipelines:          []model.PipelineSummary{{Pipeline: pipeline}},
		SelectedPipelineID: id,
		Pipeline:           pipeline,
		Stages: []model.Stage{{
			Name: "build",
			Jobs: []model.Job{{ID: 1, Name: "build-packages", Status: "running", Trace: "hello"}},
		}},
		Triggered: triggered,
		UpdatedAt: time.Unix(1711970000, 0),
	}
}
