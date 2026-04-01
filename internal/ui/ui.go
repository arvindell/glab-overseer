package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/arvindell/glab-overseer/internal/actions"
	"github.com/arvindell/glab-overseer/internal/model"
	"github.com/arvindell/glab-overseer/internal/watcher"
)

var (
	stageStyle         = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	selectedStageStyle = stageStyle.Copy().BorderForeground(lipgloss.Color("86"))
	headerStyle        = lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("57"))
	mutedStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	statusStyles       = map[string]lipgloss.Style{
		"success":  lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		"failed":   lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		"running":  lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
		"pending":  lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"created":  lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"canceled": lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
	}
)

const (
	horizontalPadding = 2
	boxChromeWidth    = 4
	defaultCycleEvery = 30 * time.Second
)

type focusMode string

const (
	focusModeDefault focusMode = "default"
	focusModeUser    focusMode = "user"
)

type eventMsg watcher.Event
type tickMsg time.Time

type modelUI struct {
	events             <-chan watcher.Event
	dispatcher         *actions.Dispatcher
	snapshot           model.Snapshot
	err                error
	width              int
	height             int
	stageIndex         int
	jobIndex           int
	selectedJobID      int64
	focusMode          focusMode
	lastPipelineID     int64
	defaultCycleIndex  int
	lastDefaultAdvance time.Time
	logViewport        viewport.Model
	now                time.Time
}

func Run(ctx context.Context, events <-chan watcher.Event, dispatcher *actions.Dispatcher) error {
	m := modelUI{
		events:      events,
		dispatcher:  dispatcher,
		now:         time.Now(),
		focusMode:   focusModeDefault,
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
		if msg.Snapshot.Pipeline.ID != 0 {
			pipelineChanged := m.lastPipelineID != 0 && m.lastPipelineID != msg.Snapshot.Pipeline.ID
			m.snapshot = msg.Snapshot
			m.lastPipelineID = msg.Snapshot.Pipeline.ID
			if pipelineChanged {
				m.activateDefaultFocus(true)
			} else {
				m.syncSelection()
			}
			m.syncViewport()
		}
		return m, waitForEvent(m.events)
	case tickMsg:
		m.now = time.Time(msg)
		if m.focusMode == focusModeDefault {
			m.advanceDefaultFocus(false)
			m.syncViewport()
		}
		return m, tick()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "F":
			m.activateDefaultFocus(true)
			m.syncViewport()
		case "left", "h":
			if m.stageIndex > 0 {
				m.focusMode = focusModeUser
				m.stageIndex--
				m.jobIndex = 0
				m.captureSelectedJobID()
				m.syncViewport()
			}
		case "right", "l":
			if m.stageIndex < len(m.snapshot.Stages)-1 {
				m.focusMode = focusModeUser
				m.stageIndex++
				m.jobIndex = 0
				m.captureSelectedJobID()
				m.syncViewport()
			}
		case "up", "k":
			if m.jobIndex > 0 {
				m.focusMode = focusModeUser
				m.jobIndex--
				m.captureSelectedJobID()
				m.syncViewport()
			}
		case "down", "j":
			if stage := m.selectedStage(); len(stage.Jobs) > 0 && m.jobIndex < len(stage.Jobs)-1 {
				m.focusMode = focusModeUser
				m.jobIndex++
				m.captureSelectedJobID()
				m.syncViewport()
			}
		case "g":
			m.logViewport.GotoTop()
		case "G":
			m.logViewport.GotoBottom()
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

	head := m.renderHeader()
	body := m.renderStages()
	m.sizeLogViewport(head, body)
	logs := m.renderLogs()

	return lipgloss.JoinVertical(lipgloss.Left, head, body, logs)
}

func (m *modelUI) resizeViewport() {
	m.logViewport.Width = max(20, m.width-horizontalPadding-boxChromeWidth)
	m.logViewport.Height = max(8, m.height/3)
}

func (m *modelUI) sizeLogViewport(head, body string) {
	usedHeight := lipgloss.Height(head) + lipgloss.Height(body)
	remainingHeight := m.height - usedHeight - 1
	m.logViewport.Height = max(8, remainingHeight-boxChromeWidth)
}

func (m *modelUI) syncViewport() {
	selected := m.selectedJob()
	content := "No job selected yet. Use left/right and up/down to inspect jobs and logs."
	if selected != nil {
		content = selected.Trace
		if content == "" {
			content = "No logs available yet for this job."
		}
	}
	m.logViewport.SetContent(content)
	m.logViewport.GotoBottom()
}

func (m *modelUI) syncSelection() {
	if len(m.snapshot.Stages) == 0 {
		m.stageIndex = 0
		m.jobIndex = 0
		m.selectedJobID = 0
		return
	}

	if m.selectedJobID != 0 {
		for stageIndex, stage := range m.snapshot.Stages {
			for jobIndex, job := range stage.Jobs {
				if job.ID == m.selectedJobID {
					m.stageIndex = stageIndex
					m.jobIndex = jobIndex
					return
				}
			}
		}
	}

	if m.stageIndex >= len(m.snapshot.Stages) {
		m.stageIndex = len(m.snapshot.Stages) - 1
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
	m.stageIndex = selected.stageIndex
	m.jobIndex = selected.jobIndex
	m.captureSelectedJobID()
	m.lastDefaultAdvance = m.now
}

func (m *modelUI) advanceDefaultFocus(force bool) {
	active := m.activeJobs()
	if len(active) == 0 {
		return
	}
	if force || m.lastDefaultAdvance.IsZero() || m.now.Sub(m.lastDefaultAdvance) >= defaultCycleEvery {
		if !force {
			m.defaultCycleIndex = (m.defaultCycleIndex + 1) % len(active)
		}
		if m.defaultCycleIndex >= len(active) {
			m.defaultCycleIndex = 0
		}
		selected := active[m.defaultCycleIndex]
		m.stageIndex = selected.stageIndex
		m.jobIndex = selected.jobIndex
		m.captureSelectedJobID()
		m.lastDefaultAdvance = m.now
	}
}

type activeJobRef struct {
	stageIndex int
	jobIndex   int
	jobID      int64
}

func (m modelUI) activeJobs() []activeJobRef {
	active := make([]activeJobRef, 0)
	for stageIndex, stage := range m.snapshot.Stages {
		for jobIndex, job := range stage.Jobs {
			if isActiveJob(job.Status) {
				active = append(active, activeJobRef{stageIndex: stageIndex, jobIndex: jobIndex, jobID: job.ID})
			}
		}
	}
	return active
}

func (m modelUI) renderHeader() string {
	if m.snapshot.Pipeline.ID == 0 {
		return headerStyle.Width(m.width).Render("Waiting for GitLab pipeline data...")
	}

	user := m.snapshot.Pipeline.UserName
	if user == "" {
		user = "unknown user"
	}
	triggered := fmt.Sprintf("Pipeline #%d triggered %s by %s", m.snapshot.Pipeline.ID, relativeTime(m.now, m.snapshot.Pipeline.CreatedAt), user)
	status := fmt.Sprintf("%s  ref:%s  source:%s  action:%s  focus:%s", m.snapshot.Pipeline.Status, m.snapshot.Pipeline.Ref, m.snapshot.Pipeline.Source, fallback(m.snapshot.ActionText, "watching"), m.focusMode)

	if m.err != nil {
		status += "  error: " + m.err.Error()
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		headerStyle.Width(m.width).Render(triggered),
		mutedStyle.Padding(0, 1).Width(m.width).Render(status),
	)
}

func (m modelUI) renderStages() string {
	if len(m.snapshot.Stages) == 0 {
		return lipgloss.NewStyle().Padding(1, 1).Render("No jobs found for the current pipeline yet.")
	}

	availableWidth := max(20, m.width-horizontalPadding)
	totalColumnWidth := max(18, availableWidth/max(1, len(m.snapshot.Stages)))
	contentWidth := max(12, totalColumnWidth-boxChromeWidth)
	columns := make([]string, 0, len(m.snapshot.Stages))

	for i, stage := range m.snapshot.Stages {
		style := stageStyle.Width(contentWidth)
		if i == m.stageIndex {
			style = selectedStageStyle.Width(contentWidth)
		}

		stageTitle := stage.Name
		if stageTitle != "" {
			stageTitle = strings.ToUpper(stageTitle[:1]) + stageTitle[1:]
		}
		lines := []string{lipgloss.NewStyle().Bold(true).Align(lipgloss.Center).Width(contentWidth).Render(stageTitle)}
		for j, job := range stage.Jobs {
			prefix := "  "
			if i == m.stageIndex && j == m.jobIndex {
				prefix = "> "
			}
			lines = append(lines, prefix+formatJob(job))
		}

		columns = append(columns, style.Render(strings.Join(lines, "\n")))
	}

	return lipgloss.NewStyle().Padding(0, 1).Render(lipgloss.JoinHorizontal(lipgloss.Top, columns...))
}

func (m modelUI) renderLogs() string {
	title := "Logs"
	if job := m.selectedJob(); job != nil {
		title = fmt.Sprintf("Logs: %s (%s)", job.Name, job.Status)
	}

	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(max(20, m.width-horizontalPadding-boxChromeWidth))
	return lipgloss.NewStyle().Padding(0, 1).Render(box.Render(title + "\n" + m.logViewport.View()))
}

func (m modelUI) selectedStage() model.Stage {
	if len(m.snapshot.Stages) == 0 || m.stageIndex >= len(m.snapshot.Stages) {
		return model.Stage{}
	}
	return m.snapshot.Stages[m.stageIndex]
}

func (m modelUI) selectedJob() *model.Job {
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
	if job.Duration > 0 {
		label += fmt.Sprintf(" [%s]", formatDuration(job.Duration))
	}
	style, ok := statusStyles[job.Status]
	if ok {
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
	case "pending", "created":
		return "○"
	case "canceled":
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

func isActiveJob(status string) bool {
	switch strings.ToLower(status) {
	case "running", "pending", "created":
		return true
	default:
		return false
	}
}
