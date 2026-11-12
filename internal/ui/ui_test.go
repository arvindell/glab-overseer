package ui

import (
	"fmt"
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

func TestOverviewFitsTerminalAndKeepsSelectedPipelineVisible(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 100, 28
	for id := int64(108); id > 100; id-- {
		m.snapshot.Pipelines = append(m.snapshot.Pipelines, model.PipelineSummary{
			Pipeline: model.Pipeline{ID: id, Ref: "main", Status: "success", CommitTitle: "A pipeline with several deployment stages"},
			Stages:   []model.StageSummary{{Name: "build", Status: "success"}, {Name: "verify", Status: "success"}, {Name: "deploy", Status: "success"}},
		})
	}
	for index, pipeline := range m.snapshot.Pipelines {
		m.overviewIndex = index
		view := m.View()
		if lipgloss.Height(view) > m.height || lipgloss.Width(view) > m.width {
			t.Fatalf("overview exceeds terminal: %dx%d > %dx%d", lipgloss.Width(view), lipgloss.Height(view), m.width, m.height)
		}
		if !strings.Contains(ansi.Strip(view), fmt.Sprintf("> #%d", pipeline.Pipeline.ID)) {
			t.Fatalf("selected pipeline %d is not visible", pipeline.Pipeline.ID)
		}
	}
}

func TestInspectWaitsForJobsAfterReceivingPipelineList(t *testing.T) {
	m := newTestModel()
	m.snapshot = model.Snapshot{SelectedPipelineID: 101, Pipelines: []model.PipelineSummary{{Pipeline: model.Pipeline{ID: 101, Ref: "main"}}}}
	m.selectedPipelineID = 101
	m, _ = m.handleKey("enter")
	if !strings.Contains(m.View(), "Loading jobs and stages") {
		t.Fatal("pipeline metadata must not be presented as a loaded empty pipeline")
	}
}

func TestNewPipelineTakesFocusBeforeItsJobsLoad(t *testing.T) {
	m := newTestModel()
	commands := make(chan watcher.Command, 4)
	m.commands = commands
	m.viewMode = viewModeInspect
	m.selectedPipelineID, m.lastPipelineID = 101, 101
	m.snapshot = makeSnapshot(101, "failed", false)
	snapshot := makeSnapshot(102, "running", true)
	snapshot.Loading = true
	snapshot.Stages = nil
	updated, _ := m.Update(eventMsg{Snapshot: snapshot})
	m = updated.(modelUI)
	if m.selectedPipelineID != 102 || m.autoInspectPipelineID != 102 || !strings.Contains(m.View(), "Loading jobs and stages") {
		t.Fatalf("new pipeline must immediately replace the old inspector: %+v", m)
	}
	updated, _ = m.Update(eventMsg{Err: fmt.Errorf("job details unavailable")})
	m = updated.(modelUI)
	if !strings.Contains(m.View(), "job details unavailable") || m.snapshot.Pipeline.ID != 102 {
		t.Fatal("loading errors must remain visible alongside the newest pipeline")
	}
	// No selection request should be sent to restore pipeline 101 while loading.
	if len(commands) != 0 {
		t.Fatalf("loading emitted an obsolete selection: %+v", <-commands)
	}
	// History inspection is still allowed, but commands identify their source view.
	m.selectedPipelineID = 101
	m.syncTraceSelection()
	command := <-commands
	if command.SelectPipelineID != 101 || command.ExpectedPipelineID != 102 {
		t.Fatalf("manual history selections must carry their source view: %+v", command)
	}
}

func TestBridgeExplainsDownstreamJobsInsteadOfLoadingNonexistentLogs(t *testing.T) {
	for _, childID := range []int64{0, 202} {
		m := newTestModel()
		m.snapshot = makeSnapshot(101, "running", false)
		m.snapshot.ParentJobs = []model.Job{{ID: 1, Name: "rollout", IsBridge: true, Status: "running", DownstreamPipelineID: childID}}
		m.parentFocus = true
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

func TestTraceSubscriptionFollowsViewedJobAndStopsInOverview(t *testing.T) {
	commands := make(chan watcher.Command, 2)
	m := newTestModel()
	m.commands = commands
	m.viewMode = viewModeInspect
	m.snapshot = makeSnapshot(101, "running", false)
	m.selectedPipelineID = 101
	m.snapshot.Stages = []model.Stage{{Name: "build", Jobs: []model.Job{{ID: 7, Status: "running"}}}}
	m.syncTraceSelection()
	m.syncTraceSelection()
	command := <-commands
	if command.SelectJobID == nil || *command.SelectJobID != 7 || command.ExpectedPipelineID != 101 {
		t.Fatal("viewed job was not subscribed")
	}
	if len(commands) != 0 {
		t.Fatal("unchanged selection emitted duplicate commands")
	}
	m.viewMode = viewModeOverview
	m.syncTraceSelection()
	command = <-commands
	if command.SelectJobID == nil || *command.SelectJobID != 0 {
		t.Fatal("overview must stop trace polling")
	}
}

func TestPipelineSelectionDoesNotBlockDuringCooldown(t *testing.T) {
	commands := make(chan watcher.Command, 1)
	commands <- watcher.Command{SelectPipelineID: 99}
	m := newTestModel()
	m.commands = commands
	m.viewMode = viewModeInspect
	m.selectedPipelineID = 102
	m.snapshot = makeSnapshot(101, "running", false)
	m.syncTraceSelection()
	<-commands
	m.syncTraceSelection()
	command := <-commands
	if command.SelectPipelineID != 102 {
		t.Fatal("selection was lost while command buffer was full")
	}
}

func TestChildJobSelectionAndLogsSurviveParentDiscovery(t *testing.T) {
	m := newTestModel()
	m.snapshot = makeSnapshot(101, "running", false)
	m.snapshot.Stages = []model.Stage{{Name: "build", Jobs: []model.Job{{ID: 7, Name: "compile", PipelineID: 303, PipelinePath: "rollout / deploy", Status: "running", Trace: "child log output"}}}}
	m.syncSelection()
	m.snapshot.ParentJobs = []model.Job{{ID: 1, Name: "orchestrate", IsBridge: true}}
	m.syncSelection()
	m.syncViewport()
	if m.parentFocus || m.stageIndex != 0 || m.selectedJobID != 7 {
		t.Fatalf("selection did not follow child job: stage=%d job=%d", m.stageIndex, m.selectedJobID)
	}
	if !strings.Contains(m.renderLogs(), "pipeline #303 (rollout / deploy)") || !strings.Contains(m.logViewport.View(), "child log output") {
		t.Fatal("expected child provenance and trace in log pane")
	}
	if !strings.Contains(formatJob(*m.selectedJob()), "[#303]") {
		t.Fatal("expected child pipeline label in job list")
	}
}

func TestParentStripDoesNotDisplacePromotionOrDeploymentColumns(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 213, 60
	m.viewMode = viewModeInspect
	m.snapshot = makeSnapshot(101, "running", false)
	m.selectedPipelineID = 101
	m.snapshot.ParentJobs = []model.Job{
		{ID: 1, Name: "start-ci-runners", Status: "success"},
		{ID: 2, Name: "orchestrate", Status: "running", IsBridge: true},
		{ID: 3, Name: "select", Status: "success"},
		{ID: 4, Name: "run-pipeline", Status: "running", IsBridge: true},
	}
	m.snapshot.Stages = nil
	for index, name := range []string{".pre", "setup", "build", "verify", "promote", "deploy"} {
		m.snapshot.Stages = append(m.snapshot.Stages, model.Stage{Name: name, Jobs: []model.Job{{ID: int64(10 + index), Name: name + "-job", Status: "created"}}})
	}
	m.syncSelection()
	view := ansi.Strip(m.View())
	for _, name := range []string{"Parents", "orchestrate", "select", "run-pipeline", "promote-job", "deploy-job"} {
		if !strings.Contains(view, name) {
			t.Fatalf("%s is missing from the Pi-sized view", name)
		}
	}
	for _, name := range []string{"orchestrate", "select", "run-pipeline"} {
		if strings.Contains(ansi.Strip(m.renderStages()), name) {
			t.Fatalf("parent %s still consumes a workload column", name)
		}
	}
}

func TestParentJobsRemainSelectableWithLogsAndStableSelection(t *testing.T) {
	m := newTestModel()
	commands := make(chan watcher.Command, 4)
	m.commands = commands
	m.viewMode = viewModeInspect
	m.snapshot = makeSnapshot(101, "running", false)
	m.selectedPipelineID = 101
	m.snapshot.ParentJobs = []model.Job{
		{ID: 2, Name: "orchestrate", Status: "running", IsBridge: true},
		{ID: 3, Name: "select", Status: "failed", Trace: "selection error", PipelinePath: "orchestrate", PipelineID: 202},
	}
	m.syncSelection()
	workloadID := m.selectedJobID
	m, _ = m.handleKey("p")
	m, _ = m.handleKey("right")
	m.syncTraceSelection()
	if !m.parentFocus || m.selectedJobID != 3 || !strings.Contains(m.logViewport.View(), "selection error") {
		t.Fatal("parent strip must preserve access to selection errors and logs")
	}
	command := <-commands
	if command.SelectJobID == nil || *command.SelectJobID != 3 {
		t.Fatal("selected parent log was not subscribed")
	}
	m.snapshot.ParentJobs = append([]model.Job{{ID: 1, Name: "bootstrap", Status: "success"}}, m.snapshot.ParentJobs...)
	m.syncSelection()
	if m.parentIndex != 2 || m.selectedJobID != 3 {
		t.Fatal("parent selection did not follow its job ID")
	}
	m, _ = m.handleKey("p")
	if m.parentFocus || m.selectedJobID != workloadID {
		t.Fatal("returning from parent strip lost the workload selection")
	}
}

func TestDefaultFocusFetchesRunningWorkloadLogsBeforeTheyExist(t *testing.T) {
	m := newTestModel()
	commands := make(chan watcher.Command, 4)
	m.commands = commands
	m.viewMode = viewModeInspect
	m.snapshot = makeSnapshot(101, "running", false)
	m.selectedPipelineID = 101
	m.snapshot.ParentJobs = []model.Job{{ID: 3, Name: "select", Status: "running", Trace: "parent output"}}
	m.snapshot.Stages = []model.Stage{{Name: "verify", Jobs: []model.Job{
		{ID: 7, Name: "e2e-tests", Status: "running"},
		{ID: 8, Name: "deploy-api", Status: "created"},
	}}}
	m.activateDefaultFocus(true)
	m.syncTraceSelection()
	if m.parentFocus || m.selectedJobID != 7 {
		t.Fatal("running workload without fetched logs was not focused before parent jobs")
	}
	command := <-commands
	if command.SelectJobID == nil || *command.SelectJobID != 7 {
		t.Fatal("running workload cannot fetch its first log")
	}
	m.snapshot.Stages[0].Jobs[0].Status = "success"
	m.snapshot.Stages[0].Jobs[1].Status = "running"
	m.now = m.now.Add(time.Second)
	m.advanceDefaultFocus(false)
	if m.selectedJobID != 8 {
		t.Fatal("focus stayed on a completed job instead of following deployment")
	}
}

func TestInspectorFitsSmallTerminalAndScrollsToSelectedWorkload(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 24
	m.viewMode = viewModeInspect
	m.snapshot = makeSnapshot(101, "running", false)
	m.selectedPipelineID = 101
	m.snapshot.ParentJobs = []model.Job{{ID: 1, Name: "orchestrate", IsBridge: true, Status: "running"}, {ID: 2, Name: "select", Status: "success"}, {ID: 3, Name: "run-pipeline", IsBridge: true, Status: "running"}}
	m.snapshot.Stages = nil
	for index, stage := range []string{"setup", "build", "verify", "promote", "deploy"} {
		jobs := make([]model.Job, 20)
		for j := range jobs {
			jobs[j] = model.Job{ID: int64(100 + index*20 + j), Name: fmt.Sprintf("%s-job-%d", stage, j), Status: "created", PipelineID: 3005331773, PipelinePath: "orchestrate / run-pipeline"}
		}
		m.snapshot.Stages = append(m.snapshot.Stages, model.Stage{Name: stage, Jobs: jobs})
	}
	for index, stage := range m.snapshot.Stages {
		m.stageIndex, m.jobIndex = index, len(stage.Jobs)-1
		m.captureSelectedJobID()
		m.syncViewport()
		view := m.View()
		if lipgloss.Width(view) > m.width || lipgloss.Height(view) > m.height {
			t.Fatalf("inspector exceeds terminal: %dx%d > %dx%d", lipgloss.Width(view), lipgloss.Height(view), m.width, m.height)
		}
		if !strings.Contains(ansi.Strip(view), stage.Jobs[len(stage.Jobs)-1].Name) {
			t.Fatal("selected workload is outside the visible stage rows")
		}
	}
}

func TestParentStripScrollsToSelectedParentWithinTerminal(t *testing.T) {
	m := newTestModel()
	m.width, m.height = 80, 24
	m.viewMode = viewModeInspect
	m.parentFocus = true
	m.snapshot = makeSnapshot(101, "running", false)
	m.selectedPipelineID = 101
	for index := range 20 {
		m.snapshot.ParentJobs = append(m.snapshot.ParentJobs, model.Job{ID: int64(100 + index), Name: fmt.Sprintf("parent-job-%02d", index), Status: "success"})
	}
	for index, parent := range m.snapshot.ParentJobs {
		m.parentIndex = index
		view := m.View()
		if lipgloss.Width(view) > m.width || lipgloss.Height(view) > m.height {
			t.Fatalf("parent strip exceeds terminal: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
		}
		if !strings.Contains(ansi.Strip(m.renderParents()), "> ✔ "+parent.Name) {
			t.Fatalf("selected parent %s is outside the visible strip", parent.Name)
		}
	}
}

func TestWaitingParentDoesNotInventMissingDeploymentJobs(t *testing.T) {
	m := newTestModel()
	m.viewMode = viewModeInspect
	m.selectedPipelineID = 101
	m.snapshot = makeSnapshot(101, "waiting_for_resource", false)
	m.snapshot.Stages = nil
	m.snapshot.ParentJobs = []model.Job{{ID: 1, Name: "bootstrap", Status: "success"}, {ID: 2, Name: "orchestrate", IsBridge: true, Status: "waiting_for_resource"}}
	m.activateDefaultFocus(true)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "orchestrate") || !strings.Contains(view, "Workload jobs have not been created yet") {
		t.Fatal("queued parent must show why workload columns are not available yet")
	}
	if !m.parentFocus || m.selectedJobID != 2 {
		t.Fatal("parent-only pipeline must focus the queued trigger, not a completed bootstrap")
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
