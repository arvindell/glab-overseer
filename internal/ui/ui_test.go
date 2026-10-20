package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/arvindell/glab-overseer/internal/model"
	"github.com/arvindell/glab-overseer/internal/watcher"
)

func TestBridgeExplainsDownstreamJobsInsteadOfLoadingNonexistentLogs(t *testing.T) {
	for _, childID := range []int64{0, 202} {
		m := newTestModel()
		m.snapshot = makeSnapshot(101, "running", false)
		m.snapshot.Stages = []model.Stage{{Name: "trigger", Jobs: []model.Job{{ID: 1, Name: "rollout", IsBridge: true, Status: "running", DownstreamPipelineID: childID}}}}
		m.syncViewport()
		content := m.logViewport.View()
		if strings.Contains(content, "Loading logs") || strings.Contains(content, "No job selected") {
			t.Fatalf("trigger should explain child state, got %s", content)
		}
		if childID == 0 && !strings.Contains(content, "Waiting for its downstream pipeline") {
			t.Fatalf("expected pending child explanation, got %s", content)
		}
		if childID != 0 && !strings.Contains(content, "downstream pipeline #202") {
			t.Fatalf("expected child pipeline identity, got %s", content)
		}
	}
}

func TestChildJobSelectionAndLogsSurviveStageDiscovery(t *testing.T) {
	m := newTestModel()
	m.snapshot = makeSnapshot(101, "running", false)
	m.snapshot.Stages = []model.Stage{{Name: "build", Jobs: []model.Job{{ID: 7, Name: "compile", PipelineID: 303, PipelinePath: "rollout / deploy", Status: "running", Trace: "child log output"}}}}
	m.syncSelection()
	m.snapshot.Stages = append([]model.Stage{{Name: "trigger", Jobs: []model.Job{{ID: 1, IsBridge: true}}}}, m.snapshot.Stages...)
	m.syncSelection()
	m.syncViewport()
	if m.stageIndex != 1 || m.selectedJobID != 7 {
		t.Fatalf("selection did not follow child job: stage=%d job=%d", m.stageIndex, m.selectedJobID)
	}
	if !strings.Contains(m.renderLogs(), "pipeline #303 (rollout / deploy)") || !strings.Contains(m.logViewport.View(), "child log output") {
		t.Fatal("expected child provenance and trace in log pane")
	}
	if !strings.Contains(formatJob(*m.selectedJob()), "[#303]") {
		t.Fatal("expected child pipeline label in job list")
	}
}

func TestStageNavigationKeepsNestedJobsVisibleWithinTerminalWidth(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.viewMode = viewModeInspect
	for _, name := range []string{"trigger", "select", "setup", "build", "verify", "deploy"} {
		m.snapshot.Stages = append(m.snapshot.Stages, model.Stage{Name: name, Jobs: []model.Job{{ID: int64(len(m.snapshot.Stages) + 1), Name: name + "-job"}}})
	}
	for index := range m.snapshot.Stages {
		m.stageIndex = index
		view := m.renderStages()
		if !strings.Contains(ansi.Strip(view), m.snapshot.Stages[index].Jobs[0].Name) {
			t.Fatalf("stage %d is not visible", index)
		}
		if lipgloss.Width(view) > m.width {
			t.Fatalf("stage view exceeds terminal width: %d > %d", lipgloss.Width(view), m.width)
		}
	}
}

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
