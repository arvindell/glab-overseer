package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/arvindell/glab-overseer/internal/actions"
	"github.com/arvindell/glab-overseer/internal/model"
	"github.com/arvindell/glab-overseer/internal/watcher"
)

var (
	stageStyle         = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	selectedStageStyle = stageStyle.Copy().BorderForeground(lipgloss.Color("86"))
	failedStageStyle   = stageStyle.Copy().BorderForeground(lipgloss.Color("196"))
	logStyle           = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	selectedLogStyle   = logStyle.Copy().BorderForeground(lipgloss.Color("86"))
	failedLogStyle     = logStyle.Copy().BorderForeground(lipgloss.Color("196"))
	headerStyle        = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("57"))
	mutedStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	spinnerFrames      = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	statusStyles       = map[string]lipgloss.Style{
		"success":              lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		"failed":               lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		"running":              lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
		"pending":              lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"created":              lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"preparing":            lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"waiting_for_resource": lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"scheduled":            lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"manual":               lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"skipped":              lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		"canceled":             lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
	}
)

const (
	horizontalPadding = 2
	boxChromeWidth    = 4
	boxChromeHeight   = 2
	logTitleHeight    = 1
	defaultCycleEvery = 10 * time.Second
)

type focusMode string

const (
	focusModeDefault focusMode = "default"
	focusModeUser    focusMode = "user"
)

type viewMode string

const (
	viewModeOverview viewMode = "overview"
	viewModeInspect  viewMode = "inspect"
)

type eventMsg watcher.Event
type tickMsg time.Time

type modelUI struct {
	events                  <-chan watcher.Event
	commands                chan<- watcher.Command
	dispatcher              *actions.Dispatcher
	snapshot                model.Snapshot
	err                     error
	width                   int
	height                  int
	viewMode                viewMode
	overviewIndex           int
	selectedPipelineID      int64
	autoInspectPipelineID   int64
	parentFocus             bool
	parentIndex             int
	stageIndex              int
	jobIndex                int
	selectedJobID           int64
	focusMode               focusMode
	lastPipelineID          int64
	defaultCycleIndex       int
	lastDefaultAdvance      time.Time
	lastRenderedJobID       int64
	lastRequestedTraceID    int64
	lastRequestedPipelineID int64
	lastRenderedContent     string
	logViewerMode           bool
	logViewport             viewport.Model
	now                     time.Time
}

func Run(ctx context.Context, events <-chan watcher.Event, commands chan<- watcher.Command, dispatcher *actions.Dispatcher) error {
	m := modelUI{
		events:      events,
		commands:    commands,
		dispatcher:  dispatcher,
		now:         time.Now(),
		focusMode:   focusModeDefault,
		viewMode:    viewModeOverview,
		logViewport: viewport.New(0, 0),
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	go func() {
		<-ctx.Done()
		p.Send(tea.Quit)
	}()

	_, err := p.Run()
	return err
}

func (m modelUI) Init() tea.Cmd {
	return tea.Batch(waitForEvent(m.events), tick())
}

func (m modelUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeViewport()
		m.syncViewport()
		return m, nil
	case eventMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, waitForEvent(m.events)
		}
		m.snapshot = msg.Snapshot
		m.err = nil
		m.syncOverviewSelection()
		if msg.Snapshot.Triggered && msg.Snapshot.Pipeline.ID != 0 {
			m.viewMode = viewModeInspect
			m.selectedPipelineID = msg.Snapshot.Pipeline.ID
			m.autoInspectPipelineID = msg.Snapshot.Pipeline.ID
		}
		if msg.Snapshot.Pipeline.ID != 0 {
			pipelineChanged := m.lastPipelineID != 0 && m.lastPipelineID != msg.Snapshot.Pipeline.ID
			m.lastPipelineID = msg.Snapshot.Pipeline.ID
			if pipelineChanged || m.selectedPipelineID != msg.Snapshot.SelectedPipelineID {
				m.selectedPipelineID = msg.Snapshot.SelectedPipelineID
				m.resetInspectSelection()
			} else {
				m.syncSelection()
			}
			m.syncViewport()
			if m.autoInspectPipelineID != 0 && m.snapshot.Pipeline.ID == m.autoInspectPipelineID && strings.ToLower(m.snapshot.Pipeline.Status) == "success" {
				m.viewMode = viewModeOverview
				m.autoInspectPipelineID = 0
				m.logViewerMode = false
			}
		}
		m.syncTraceSelection()
		return m, waitForEvent(m.events)
	case tickMsg:
		m.now = time.Time(msg)
		if m.focusMode == focusModeDefault {
			m.advanceDefaultFocus(false)
			m.syncViewport()
		}
		m.syncTraceSelection()
		return m, tick()
	case tea.KeyMsg:
		if updated, cmd := m.handleKey(msg.String()); true {
			m = updated
			m.syncTraceSelection()
			if cmd != nil {
				return m, cmd
			}
		}
	}

	var cmd tea.Cmd
	m.logViewport, cmd = m.logViewport.Update(msg)
	return m, cmd
}

func (m modelUI) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}
	if m.viewMode == viewModeOverview {
		return m.renderOverview()
	}

	head := m.renderHeader()
	if parents := m.renderParents(); parents != "" {
		head = lipgloss.JoinVertical(lipgloss.Left, head, parents)
	}
	body := m.renderStages()
	m.sizeLogViewport(head, body)
	logs := m.renderLogs()

	return lipgloss.JoinVertical(lipgloss.Left, head, body, logs)
}

func (m *modelUI) syncTraceSelection() {
	if m.commands == nil {
		return
	}
	if m.viewMode == viewModeInspect && m.selectedPipelineID != 0 && m.snapshot.Pipeline.ID != m.selectedPipelineID {
		if m.lastRequestedPipelineID != m.selectedPipelineID {
			select {
			case m.commands <- watcher.Command{ExpectedPipelineID: m.snapshot.SelectedPipelineID, SelectPipelineID: m.selectedPipelineID}:
				m.lastRequestedPipelineID = m.selectedPipelineID
			default:
			}
		}
		return
	}
	m.lastRequestedPipelineID = 0
	var jobID int64
	if m.viewMode == viewModeInspect && m.snapshot.Pipeline.ID == m.selectedPipelineID {
		if job := m.selectedJob(); job != nil && !job.IsBridge {
			jobID = job.ID
		}
	}
	if jobID == m.lastRequestedTraceID {
		return
	}
	select {
	case m.commands <- watcher.Command{ExpectedPipelineID: m.snapshot.SelectedPipelineID, SelectJobID: &jobID}:
		m.lastRequestedTraceID = jobID
	default:
	}
}

func (m *modelUI) resizeViewport() {
	m.logViewport.Width = max(1, m.logContentWidth())
	m.logViewport.Height = max(1, m.height/3)
}

func (m *modelUI) sizeLogViewport(head, body string) {
	usedHeight := lipgloss.Height(head) + lipgloss.Height(body)
	remainingHeight := m.height - usedHeight
	availableHeight := remainingHeight - logTitleHeight - boxChromeHeight
	if availableHeight < 1 {
		availableHeight = 1
	}
	m.logViewport.Height = availableHeight
}

func (m *modelUI) syncViewport() {
	selected := m.selectedJob()
	content := "No jobs or child pipelines are available for this pipeline yet."
	selectedID := int64(0)
	if selected != nil {
		selectedID = selected.ID
		content = sanitizeLogContent(selected.Trace)
		if selected.IsBridge {
			if selected.DownstreamPipelineID == 0 {
				content = fmt.Sprintf("Trigger job %s (%s). Waiting for its downstream pipeline to be created.", selected.Name, selected.Status)
			} else {
				content = fmt.Sprintf("Trigger job %s (%s) starts downstream pipeline #%d. Its jobs are included above. Trigger jobs do not have logs; select a downstream job to view its logs.", selected.Name, selected.Status, selected.DownstreamPipelineID)
			}
		} else if content == "" {
			if shouldShowLogSpinner(*selected) {
				content = fmt.Sprintf("%s Loading logs for %s...", m.spinnerFrame(), selected.Name)
			} else {
				content = "No logs available yet for this job."
			}
		}
	}
	wrapped := m.wrapLogContent(content)
	if wrapped == m.lastRenderedContent && selectedID == m.lastRenderedJobID {
		return
	}

	wasAtBottom := m.logViewport.AtBottom()
	previousOffset := m.logViewport.YOffset
	m.logViewport.SetContent(wrapped)

	if !m.logViewerMode || selectedID != m.lastRenderedJobID || wasAtBottom {
		m.logViewport.GotoBottom()
	} else {
		maxOffset := max(0, m.logViewport.TotalLineCount()-m.logViewport.Height)
		if previousOffset > maxOffset {
			previousOffset = maxOffset
		}
		m.logViewport.SetYOffset(previousOffset)
	}

	m.lastRenderedContent = wrapped
	m.lastRenderedJobID = selectedID
}

func (m *modelUI) syncOverviewSelection() {
	if len(m.snapshot.Pipelines) == 0 {
		m.overviewIndex = 0
		if m.selectedPipelineID == 0 {
			m.selectedPipelineID = m.snapshot.SelectedPipelineID
		}
		return
	}

	targetID := m.selectedPipelineID
	if targetID == 0 {
		targetID = m.snapshot.SelectedPipelineID
	}
	if targetID == 0 {
		targetID = m.snapshot.Pipelines[0].Pipeline.ID
	}

	for i, pipeline := range m.snapshot.Pipelines {
		if pipeline.Pipeline.ID == targetID {
			m.overviewIndex = i
			m.selectedPipelineID = targetID
			return
		}
	}

	if m.overviewIndex >= len(m.snapshot.Pipelines) {
		m.overviewIndex = len(m.snapshot.Pipelines) - 1
	}
	m.selectedPipelineID = m.snapshot.Pipelines[m.overviewIndex].Pipeline.ID
}

func (m *modelUI) resetInspectSelection() {
	m.focusMode = focusModeDefault
	m.stageIndex = 0
	m.jobIndex = 0
	m.parentIndex = 0
	m.parentFocus = false
	m.selectedJobID = 0
	m.logViewerMode = false
	m.activateDefaultFocus(true)
}

func (m modelUI) handleKey(key string) (modelUI, tea.Cmd) {
	switch m.viewMode {
	case viewModeOverview:
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.overviewIndex > 0 {
				m.overviewIndex--
				m.selectedPipelineID = m.snapshot.Pipelines[m.overviewIndex].Pipeline.ID
			}
		case "down", "j":
			if len(m.snapshot.Pipelines) > 0 && m.overviewIndex < len(m.snapshot.Pipelines)-1 {
				m.overviewIndex++
				m.selectedPipelineID = m.snapshot.Pipelines[m.overviewIndex].Pipeline.ID
			}
		case "enter":
			if len(m.snapshot.Pipelines) == 0 {
				return m, nil
			}
			m.viewMode = viewModeInspect
			m.selectedPipelineID = m.snapshot.Pipelines[m.overviewIndex].Pipeline.ID
			m.autoInspectPipelineID = 0
			m.resetInspectSelection()
		}
		return m, nil
	default:
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "b":
			m.viewMode = viewModeOverview
			m.logViewerMode = false
			m.autoInspectPipelineID = 0
			return m, nil
		case "esc":
			if m.logViewerMode {
				m.logViewerMode = false
				m.syncViewport()
			} else {
				m.viewMode = viewModeOverview
				m.autoInspectPipelineID = 0
			}
		case "F":
			m.logViewerMode = false
			m.activateDefaultFocus(true)
			m.syncViewport()
		case "p":
			if len(m.snapshot.ParentJobs) > 0 {
				m.parentFocus = !m.parentFocus || len(m.snapshot.Stages) == 0
				m.focusMode = focusModeUser
				m.logViewerMode = false
				m.captureSelectedJobID()
				m.syncViewport()
			}
		case "enter":
			m.logViewerMode = true
			m.syncViewport()
		case "left", "h", "right", "l", "up", "k", "down", "j":
			m.logViewerMode = false
			m.focusMode = focusModeUser
			if m.parentFocus {
				direction := 1
				if key == "left" || key == "h" || key == "up" || key == "k" {
					direction = -1
				}
				m.parentIndex = max(0, min(len(m.snapshot.ParentJobs)-1, m.parentIndex+direction))
			} else {
				switch key {
				case "left", "h":
					m.stageIndex = max(0, m.stageIndex-1)
					m.jobIndex = 0
				case "right", "l":
					m.stageIndex = max(0, min(len(m.snapshot.Stages)-1, m.stageIndex+1))
					m.jobIndex = 0
				case "up", "k":
					m.jobIndex = max(0, m.jobIndex-1)
				case "down", "j":
					m.jobIndex = max(0, min(len(m.selectedStage().Jobs)-1, m.jobIndex+1))
				}
			}
			m.captureSelectedJobID()
			m.syncViewport()
		case "g":
			if m.logViewerMode {
				m.logViewport.GotoTop()
			}
		case "G":
			if m.logViewerMode {
				m.logViewport.GotoBottom()
			}
		case "pgup", "ctrl+u":
			if m.logViewerMode {
				m.logViewport.HalfPageUp()
			}
		case "pgdown", "ctrl+d":
			if m.logViewerMode {
				m.logViewport.HalfPageDown()
			}
		case "home":
			if m.logViewerMode {
				m.logViewport.GotoTop()
			}
		case "end":
			if m.logViewerMode {
				m.logViewport.GotoBottom()
			}
		}
		return m, nil
	}
}

func (m modelUI) wrapLogContent(content string) string {
	if m.logViewport.Width <= 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, strings.Split(ansi.Hardwrap(line, m.logViewport.Width, true), "\n")...)
	}
	return strings.Join(wrapped, "\n")
}

func (m *modelUI) syncSelection() {
	if m.selectedJobID != 0 {
		for stageIndex, stage := range m.snapshot.Stages {
			for jobIndex, job := range stage.Jobs {
				if job.ID == m.selectedJobID {
					m.parentFocus = false
					m.stageIndex = stageIndex
					m.jobIndex = jobIndex
					return
				}
			}
		}
		for index, job := range m.snapshot.ParentJobs {
			if job.ID == m.selectedJobID {
				m.parentFocus = true
				m.parentIndex = index
				return
			}
		}
	}
	if len(m.snapshot.Stages) == 0 {
		m.parentFocus = len(m.snapshot.ParentJobs) > 0
		m.stageIndex, m.jobIndex = 0, 0
	}
	if m.parentFocus && len(m.snapshot.ParentJobs) > 0 {
		m.parentIndex = min(m.parentIndex, len(m.snapshot.ParentJobs)-1)
		m.captureSelectedJobID()
		return
	}
	m.parentFocus = false

	if m.stageIndex >= len(m.snapshot.Stages) {
		m.stageIndex = max(0, len(m.snapshot.Stages)-1)
	}
	if stage := m.selectedStage(); m.jobIndex >= len(stage.Jobs) {
		m.jobIndex = max(0, len(stage.Jobs)-1)
	}
	m.captureSelectedJobID()
}

func (m *modelUI) captureSelectedJobID() {
	if job := m.selectedJob(); job != nil {
		m.selectedJobID = job.ID
		return
	}
	m.selectedJobID = 0
}

func (m *modelUI) activateDefaultFocus(reset bool) {
	m.focusMode = focusModeDefault
	if reset {
		m.parentFocus = len(m.snapshot.Stages) == 0
		m.selectedJobID = 0
	}
	active := m.activeJobs()
	if len(active) == 0 {
		if reset {
			m.defaultCycleIndex = 0
			m.lastDefaultAdvance = m.now
		}
		m.syncSelection()
		return
	}
	if reset || m.defaultCycleIndex >= len(active) {
		m.defaultCycleIndex = 0
	}
	selected := active[m.defaultCycleIndex]
	m.focusJob(selected)
	m.lastDefaultAdvance = m.now
}

func (m *modelUI) advanceDefaultFocus(force bool) {
	active := m.activeJobs()
	if len(active) == 0 {
		return
	}
	currentIndex := -1
	for index, job := range active {
		if job.jobID == m.selectedJobID {
			currentIndex = index
			break
		}
	}
	if force || currentIndex < 0 || m.lastDefaultAdvance.IsZero() || m.now.Sub(m.lastDefaultAdvance) >= defaultCycleEvery {
		if !force {
			m.defaultCycleIndex = (currentIndex + 1) % len(active)
		}
		if m.defaultCycleIndex >= len(active) {
			m.defaultCycleIndex = 0
		}
		selected := active[m.defaultCycleIndex]
		m.focusJob(selected)
		m.lastDefaultAdvance = m.now
	}
}

type activeJobRef struct {
	parent      bool
	parentIndex int
	stageIndex  int
	jobIndex    int
	jobID       int64
}

func (m *modelUI) focusJob(job activeJobRef) {
	m.parentFocus = job.parent
	if job.parent {
		m.parentIndex = job.parentIndex
	} else {
		m.stageIndex, m.jobIndex = job.stageIndex, job.jobIndex
	}
	m.captureSelectedJobID()
}

func (m modelUI) activeJobs() []activeJobRef {
	active := make([]activeJobRef, 0)
	for stageIndex, stage := range m.snapshot.Stages {
		for jobIndex, job := range stage.Jobs {
			if isCyclableRunningJob(job) {
				active = append(active, activeJobRef{stageIndex: stageIndex, jobIndex: jobIndex, jobID: job.ID})
			}
		}
	}
	if len(active) == 0 {
		for index, job := range m.snapshot.ParentJobs {
			if isCyclableRunningJob(job) {
				active = append(active, activeJobRef{parent: true, parentIndex: index, jobID: job.ID})
			}
		}
	}
	if len(active) == 0 && len(m.snapshot.Stages) == 0 {
		for index, job := range m.snapshot.ParentJobs {
			switch strings.ToLower(job.Status) {
			case "running", "waiting_for_resource", "pending", "created", "preparing":
				active = append(active, activeJobRef{parent: true, parentIndex: index, jobID: job.ID})
			}
		}
	}
	return active
}

func (m modelUI) renderHeader() string {
	if m.isInspectLoading() {
		return lipgloss.JoinVertical(lipgloss.Left,
			headerStyle.Width(m.width).Render(fmt.Sprintf("Loading Pipeline #%d", m.selectedPipelineID)),
			mutedStyle.Padding(0, 1).Width(m.width).Render("Fetching pipeline details..."),
		)
	}
	if m.snapshot.Pipeline.ID == 0 {
		return headerStyle.Width(m.width).Render("Waiting for GitLab pipeline data...")
	}

	user := m.snapshot.Pipeline.UserName
	if user == "" {
		user = "unknown user"
	}
	triggered := fmt.Sprintf("Pipeline #%d triggered %s by %s", m.snapshot.Pipeline.ID, relativeTime(m.now, m.snapshot.Pipeline.CreatedAt), user)
	status := fmt.Sprintf("%s  ref:%s  source:%s  action:%s  focus:%s", m.snapshot.Pipeline.Status, m.snapshot.Pipeline.Ref, m.snapshot.Pipeline.Source, fallback(m.snapshot.ActionText, "watching"), m.focusMode)
	if m.logViewerMode {
		status += "  logs:viewer"
	} else {
		status += "  logs:preview"
	}

	if m.err != nil {
		status += "  error: " + m.err.Error()
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		headerStyle.Width(m.width).Render(triggered),
		mutedStyle.Padding(0, 1).Width(m.width).Render(status),
	)
}

func (m modelUI) renderOverview() string {
	head := headerStyle.Width(m.width).Render(fmt.Sprintf("%s Pipeline Overview", fallback(m.snapshot.Project, "glab-overseer")))
	text := "Recent pipelines. Use ↑/↓ to select and Enter to inspect. Press b in inspect mode to return."
	if m.err != nil {
		text += "\n" + m.err.Error()
	}
	subtitle := mutedStyle.Padding(0, 1).Width(m.width).Render(text)

	if len(m.snapshot.Pipelines) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, head, subtitle, lipgloss.NewStyle().Padding(1, 1).Render(fmt.Sprintf("%s Loading pipelines...", m.spinnerFrame())))
	}

	leftWidth := max(32, m.width/2)
	rightWidth := max(32, m.width-leftWidth-horizontalPadding)
	bodyHeight := max(1, m.height-lipgloss.Height(head)-lipgloss.Height(subtitle))
	left := m.renderOverviewList(leftWidth, bodyHeight)
	right := lipgloss.NewStyle().MaxHeight(bodyHeight).Render(m.renderOverviewDetails(rightWidth))
	body := lipgloss.NewStyle().Padding(0, 1).Render(lipgloss.JoinHorizontal(lipgloss.Top, left, right))

	return lipgloss.JoinVertical(lipgloss.Left, head, subtitle, body)
}

func (m modelUI) renderOverviewList(width, height int) string {
	rows := make([]string, 0, len(m.snapshot.Pipelines))
	for i, item := range m.snapshot.Pipelines {
		prefix := "  "
		style := stageStyle.Width(max(20, width-boxChromeWidth))
		if i == m.overviewIndex {
			prefix = "> "
			style = selectedStageStyle.Width(max(20, width-boxChromeWidth))
		}

		rowTitle := fmt.Sprintf("%s#%d  %s", prefix, item.Pipeline.ID, item.Pipeline.Ref)
		commitTitle := fallback(item.Pipeline.CommitTitle, "No commit title")
		meta := fmt.Sprintf("%s  by %s  %s  %s", item.Pipeline.Status, fallback(item.Pipeline.UserName, "unknown"), shortSHA(item.Pipeline.SHA), relativeTime(m.now, item.Pipeline.CreatedAt))
		stages := make([]string, 0, len(item.Stages))
		for _, stage := range item.Stages {
			stages = append(stages, formatStageBadge(stage))
		}

		headline := lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Bold(true).Render(rowTitle),
			"  ",
			mutedStyle.Render(meta),
		)

		lines := []string{headline, commitTitle}
		if len(item.ParentJobs) > 0 {
			lines = append(lines, "Parents: "+formatParentJobs(item.ParentJobs, -1))
		}
		if len(stages) > 0 {
			lines = append(lines, strings.Join(stages, " "))
		} else {
			lines = append(lines, "Workload jobs not created yet.")
		}
		rows = append(rows, style.Render(strings.Join(lines, "\n")))
	}
	list := viewport.New(width, height)
	list.SetContent(lipgloss.JoinVertical(lipgloss.Left, rows...))
	selectedTop := 0
	for i, row := range rows {
		if i == m.overviewIndex {
			list.SetYOffset(max(0, selectedTop+min(height, lipgloss.Height(row))-height))
			break
		}
		selectedTop += lipgloss.Height(row)
	}
	return list.View()
}

func (m modelUI) renderOverviewDetails(width int) string {
	if len(m.snapshot.Pipelines) == 0 || m.overviewIndex >= len(m.snapshot.Pipelines) {
		return ""
	}
	item := m.snapshot.Pipelines[m.overviewIndex]
	box := selectedLogStyle.Width(max(20, width-boxChromeWidth))
	lines := []string{
		fmt.Sprintf("Pipeline #%d", item.Pipeline.ID),
		fmt.Sprintf("Branch: %s", item.Pipeline.Ref),
		fmt.Sprintf("Status: %s", item.Pipeline.Status),
		fmt.Sprintf("Author: %s", fallback(item.Pipeline.UserName, "unknown")),
		fmt.Sprintf("Commit: %s", fallback(item.Pipeline.CommitTitle, "unknown")),
		fmt.Sprintf("SHA: %s", fallback(shortSHA(item.Pipeline.SHA), "unknown")),
		fmt.Sprintf("Source: %s", item.Pipeline.Source),
		fmt.Sprintf("Started: %s", relativeTime(m.now, item.Pipeline.CreatedAt)),
		"",
	}
	if len(item.ParentJobs) > 0 {
		lines = append(lines, "Parents: "+formatParentJobs(item.ParentJobs, -1), "")
	}
	lines = append(lines, "Workload Stages")
	for _, stage := range item.Stages {
		lines = append(lines, fmt.Sprintf("%s %s", statusIcon(stage.Status), strings.ToUpper(firstChar(stage.Name))+restChars(stage.Name)))
	}
	if len(item.Stages) == 0 {
		lines = append(lines, "Workload jobs not created yet.")
	}
	lines = append(lines, "", "Enter to inspect this pipeline")
	return box.Render(strings.Join(lines, "\n"))
}

func (m modelUI) renderParents() string {
	if m.isInspectLoading() || len(m.snapshot.ParentJobs) == 0 {
		return ""
	}
	selected := -1
	label := fmt.Sprintf("Parents (%d)", len(m.snapshot.ParentJobs))
	failed := 0
	for _, job := range m.snapshot.ParentJobs {
		if strings.ToLower(job.Status) == "failed" {
			failed++
		}
	}
	if failed > 0 {
		label += fmt.Sprintf(" · ✖ %d failed", failed)
	}
	if m.parentFocus {
		selected = m.parentIndex
		label += " · ←/→ select · p back to workload stages"
	} else {
		label += " · p to inspect"
	}
	width := max(1, m.width-horizontalPadding)
	rows := []string{}
	row, selectedRow := "", 0
	for index, job := range m.snapshot.ParentJobs {
		focus := -1
		if index == selected {
			focus = 0
		}
		badge := ansi.Truncate(formatParentJobs([]model.Job{job}, focus), width, "…")
		if row != "" && lipgloss.Width(row)+5+lipgloss.Width(badge) > width {
			rows = append(rows, row)
			row = ""
		}
		if index == selected {
			selectedRow = len(rows)
		}
		if row != "" {
			row += "  ·  "
		}
		row += badge
	}
	rows = append(rows, row)
	content := strings.Join(rows, "\n")
	height := min(len(rows), max(1, min(2, m.height/12)))
	strip := viewport.New(width, height)
	strip.SetContent(content)
	if selected >= 0 {
		strip.SetYOffset(max(0, selectedRow-height+1))
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(mutedStyle.Render(ansi.Truncate(label, width, "…")) + "\n" + strip.View())
}

func formatParentJobs(jobs []model.Job, selected int) string {
	labels := make([]string, 0, len(jobs))
	for index, job := range jobs {
		label := statusIcon(job.Status) + " "
		if job.IsBridge {
			label += "↳ "
		}
		label += job.Name
		if index == selected {
			label = "> " + label
			label = lipgloss.NewStyle().Bold(true).Underline(true).Render(label)
		}
		if style, ok := statusStyles[job.Status]; ok {
			label = style.Render(label)
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, "  ·  ")
}

func (m modelUI) renderStages() string {
	if m.isInspectLoading() {
		return lipgloss.NewStyle().Padding(1, 1).Render(fmt.Sprintf("%s Loading jobs and stages...", m.spinnerFrame()))
	}
	if len(m.snapshot.Stages) == 0 {
		if len(m.snapshot.ParentJobs) > 0 {
			return lipgloss.NewStyle().Padding(1, 1).Render("Workload jobs have not been created yet; see parent status above.")
		}
		return lipgloss.NewStyle().Padding(1, 1).Render("No jobs or child pipelines are available yet.")
	}

	availableWidth := max(20, m.width-horizontalPadding)
	columnCount := min(len(m.snapshot.Stages), max(1, availableWidth/28))
	firstStage := min(max(0, m.stageIndex-columnCount/2), len(m.snapshot.Stages)-columnCount)
	totalColumnWidth := max(18, availableWidth/columnCount)
	contentWidth := max(12, totalColumnWidth-boxChromeWidth)
	columns := make([]string, 0, columnCount)
	navigationHeight := 0
	if columnCount < len(m.snapshot.Stages) {
		navigationHeight = 1
	}
	maxRows := max(1, m.height-lipgloss.Height(m.renderHeader())-lipgloss.Height(m.renderParents())-navigationHeight-1-boxChromeHeight-logTitleHeight-boxChromeHeight-3)

	for i := firstStage; i < firstStage+columnCount; i++ {
		stage := m.snapshot.Stages[i]
		style := stageStyle.Width(contentWidth)
		if m.isInspectFailed() {
			style = failedStageStyle.Width(contentWidth)
		} else if !m.parentFocus && i == m.stageIndex {
			style = selectedStageStyle.Width(contentWidth)
		}

		stageTitle := stage.Name
		if stageTitle != "" {
			stageTitle = strings.ToUpper(stageTitle[:1]) + stageTitle[1:]
		}
		lines := make([]string, 0, len(stage.Jobs))
		for j, job := range stage.Jobs {
			prefix := "  "
			if !m.parentFocus && i == m.stageIndex && j == m.jobIndex {
				prefix = "> "
			}
			// Provenance stays in the log heading instead of wrapping every job row.
			job.PipelinePath = ""
			lines = append(lines, ansi.Truncate(prefix+formatJob(job), contentWidth, "…"))
		}
		jobs := viewport.New(contentWidth, min(maxRows, max(1, len(lines))))
		jobs.SetContent(strings.Join(lines, "\n"))
		if i == m.stageIndex {
			jobs.SetYOffset(max(0, m.jobIndex-jobs.Height+1))
		}
		if len(lines) > jobs.Height {
			stageTitle += fmt.Sprintf(" (%d jobs)", len(lines))
		}
		title := lipgloss.NewStyle().Bold(true).Align(lipgloss.Center).Width(contentWidth).Render(ansi.Truncate(stageTitle, contentWidth, "…"))
		columns = append(columns, style.Render(title+"\n"+jobs.View()))
	}

	body := lipgloss.NewStyle().Padding(0, 1).Render(lipgloss.JoinHorizontal(lipgloss.Top, columns...))
	if columnCount < len(m.snapshot.Stages) {
		heading := fmt.Sprintf("Workload stages %d–%d of %d · ←/→ to navigate", firstStage+1, firstStage+columnCount, len(m.snapshot.Stages))
		heading = ansi.Truncate(heading, availableWidth, "…")
		return mutedStyle.Padding(0, 1).Render(heading) + "\n" + body
	}
	return body
}

func (m modelUI) renderLogs() string {
	if m.isInspectLoading() {
		return lipgloss.NewStyle().Padding(0, 1).Render("Logs\n" + logStyle.Width(m.logBoxWidth()).Render(fmt.Sprintf("%s Loading logs...", m.spinnerFrame())))
	}
	title := "Logs"
	if job := m.selectedJob(); job != nil {
		title = fmt.Sprintf("Logs: %s (%s)", job.Name, job.Status)
		if job.PipelinePath != "" {
			title += fmt.Sprintf(" · pipeline #%d (%s)", job.PipelineID, job.PipelinePath)
		}
	}
	if m.logViewerMode {
		title += " [viewer]"
	} else {
		title += " [preview]"
	}
	title = ansi.Truncate(title, max(1, m.width-horizontalPadding), "…")

	box := logStyle.Width(m.logBoxWidth())
	if m.isInspectFailed() {
		box = failedLogStyle.Width(m.logBoxWidth())
	} else if m.logViewerMode {
		box = selectedLogStyle.Width(m.logBoxWidth())
	}
	view := m.logViewport.View()
	if !m.logViewerMode {
		vp := m.logViewport
		vp.GotoBottom()
		view = vp.View()
	}
	return lipgloss.NewStyle().Padding(0, 1).Render(title + "\n" + box.Render(view))
}

func (m modelUI) isInspectLoading() bool {
	return m.viewMode == viewModeInspect && m.selectedPipelineID != 0 && (m.snapshot.Loading || m.selectedPipelineID != m.snapshot.Pipeline.ID)
}

func (m modelUI) isInspectFailed() bool {
	return m.viewMode == viewModeInspect && strings.ToLower(m.snapshot.Pipeline.Status) == "failed"
}

func (m modelUI) logBoxWidth() int {
	return max(1, m.logContentWidth())
}

func (m modelUI) logContentWidth() int {
	return max(1, m.width-horizontalPadding-boxChromeWidth)
}

func (m modelUI) selectedStage() model.Stage {
	if len(m.snapshot.Stages) == 0 || m.stageIndex >= len(m.snapshot.Stages) {
		return model.Stage{}
	}
	return m.snapshot.Stages[m.stageIndex]
}

func (m modelUI) selectedJob() *model.Job {
	if m.parentFocus {
		if m.parentIndex >= 0 && m.parentIndex < len(m.snapshot.ParentJobs) {
			job := m.snapshot.ParentJobs[m.parentIndex]
			return &job
		}
		return nil
	}
	stage := m.selectedStage()
	if len(stage.Jobs) == 0 || m.jobIndex >= len(stage.Jobs) {
		return nil
	}
	job := stage.Jobs[m.jobIndex]
	return &job
}

func waitForEvent(events <-chan watcher.Event) tea.Cmd {
	return func() tea.Msg {
		return eventMsg(<-events)
	}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func formatJob(job model.Job) string {
	icon := statusIcon(job.Status)
	label := fmt.Sprintf("%s %s", icon, job.Name)
	if job.IsBridge {
		label = fmt.Sprintf("%s ↳ %s", icon, job.Name)
	}
	if job.PipelinePath != "" {
		label += fmt.Sprintf(" [#%d]", job.PipelineID)
	}
	if job.Duration > 0 {
		label += fmt.Sprintf(" [%s]", formatDuration(job.Duration))
	}
	style, ok := statusStyles[job.Status]
	if ok {
		return style.Render(label)
	}
	return label
}

func formatStageBadge(stage model.StageSummary) string {
	label := fmt.Sprintf("%s %s", statusIcon(stage.Status), strings.ToUpper(firstChar(stage.Name))+restChars(stage.Name))
	if style, ok := statusStyles[stage.Status]; ok {
		return style.Render(label)
	}
	return label
}

func statusIcon(status string) string {
	switch status {
	case "success":
		return "✔"
	case "failed":
		return "✖"
	case "running":
		return "●"
	case "pending", "created", "preparing", "waiting_for_resource", "scheduled", "manual":
		return "○"
	case "canceled", "skipped":
		return "-"
	default:
		return "?"
	}
}

func relativeTime(now, t time.Time) string {
	if t.IsZero() {
		return "just now"
	}
	delta := now.Sub(t).Round(time.Second)
	if delta < time.Minute {
		return fmt.Sprintf("%ds ago", int(delta.Seconds()))
	}
	if delta < time.Hour {
		return fmt.Sprintf("%dm ago", int(delta.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(delta.Hours()))
}

func formatDuration(duration time.Duration) string {
	if duration < time.Minute {
		return fmt.Sprintf("%02ds", int(duration.Seconds()))
	}
	minutes := int(duration / time.Minute)
	seconds := int((duration % time.Minute) / time.Second)
	return fmt.Sprintf("%02dm %02ds", minutes, seconds)
}

func fallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func shortSHA(value string) string {
	if len(value) >= 8 {
		return value[:8]
	}
	return value
}

func firstChar(value string) string {
	if value == "" {
		return ""
	}
	runes := []rune(value)
	return string(runes[:1])
}

func restChars(value string) string {
	runes := []rune(value)
	if len(runes) <= 1 {
		return ""
	}
	return string(runes[1:])
}

func (m modelUI) spinnerFrame() string {
	if len(spinnerFrames) == 0 {
		return "..."
	}
	index := int(m.now.UnixNano()/int64(100*time.Millisecond)) % len(spinnerFrames)
	if index < 0 {
		index = 0
	}
	return spinnerFrames[index]
}

func shouldShowLogSpinner(job model.Job) bool {
	if strings.TrimSpace(job.Trace) != "" {
		return false
	}

	if job.TraceSize > 0 {
		return false
	}

	switch strings.ToLower(job.Status) {
	case "running", "pending", "created", "success", "failed", "canceled":
		return true
	default:
		return false
	}
}

func isCyclableRunningJob(job model.Job) bool {
	return !job.IsBridge && strings.ToLower(job.Status) == "running"
}

func sanitizeLogContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return content
}
