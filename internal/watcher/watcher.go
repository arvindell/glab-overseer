package watcher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/arvindell/glab-overseer/internal/actions"
	"github.com/arvindell/glab-overseer/internal/config"
	"github.com/arvindell/glab-overseer/internal/gitlab"
	"github.com/arvindell/glab-overseer/internal/model"
	"github.com/arvindell/glab-overseer/internal/state"
)

type Event struct {
	Snapshot model.Snapshot
	Err      error
}

type Command struct {
	SelectPipelineID int64
	SelectJobID      *int64
}

type cachedPipeline struct {
	pipeline    model.Pipeline
	jobs        []model.Job
	refreshedAt time.Time
}

type Watcher struct {
	client     *gitlab.Client
	store      *state.Store
	dispatcher *actions.Dispatcher
	cfg        config.Config
	commands   <-chan Command

	projectID      int64
	traceMu        sync.Mutex
	traceSize      map[int64]int64
	traces         map[int64]string
	currentID      int64
	overview       []model.PipelineSummary
	pipelines      map[int64]cachedPipeline
	commitTitles   map[string]string
	selectedJobs   []model.Job
	traceJobID     int64
	finishedTraces map[int64]string
}

func New(client *gitlab.Client, store *state.Store, dispatcher *actions.Dispatcher, cfg config.Config, commands <-chan Command) *Watcher {
	return &Watcher{
		client:         client,
		store:          store,
		dispatcher:     dispatcher,
		cfg:            cfg,
		commands:       commands,
		traceSize:      map[int64]int64{},
		traces:         map[int64]string{},
		pipelines:      map[int64]cachedPipeline{},
		commitTitles:   map[string]string{},
		finishedTraces: map[int64]string{},
	}
}

func (w *Watcher) Run(ctx context.Context, events chan<- Event) {
	for {
		projectID, err := w.client.ResolveProjectID(ctx, w.cfg.Project)
		if err == nil {
			w.projectID = projectID
			break
		}
		events <- Event{Err: err}
		var limited *gitlab.RateLimitError
		if !errors.As(err, &limited) {
			return
		}
	}

	if err := w.refresh(ctx, events); err != nil {
		events <- Event{Err: err}
	}

	pipelineTicker := time.NewTicker(w.cfg.PollInterval)
	traceTicker := time.NewTicker(w.cfg.TraceInterval)
	defer pipelineTicker.Stop()
	defer traceTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-w.commands:
			// Selections can change while the client waits for a quota reset.
			// Fetch only the latest requested pipeline/log when requests resume.
			for len(w.commands) > 0 {
				next := <-w.commands
				if next.SelectPipelineID != 0 {
					cmd.SelectPipelineID = next.SelectPipelineID
				}
				if next.SelectJobID != nil {
					cmd.SelectJobID = next.SelectJobID
				}
			}
			if cmd.SelectPipelineID != 0 {
				w.currentID = cmd.SelectPipelineID
				if err := w.refresh(ctx, events); err != nil {
					events <- Event{Err: err}
				}
			}
			if cmd.SelectJobID != nil {
				w.traceJobID = *cmd.SelectJobID
				if err := w.refreshTraces(ctx, events); err != nil {
					events <- Event{Err: err}
				}
			}
		case <-pipelineTicker.C:
			if err := w.refresh(ctx, events); err != nil {
				events <- Event{Err: err}
			}
		case <-traceTicker.C:
			if err := w.refreshTraces(ctx, events); err != nil {
				events <- Event{Err: err}
			}
		}
	}
}

func (w *Watcher) refresh(ctx context.Context, events chan<- Event) error {
	recent, err := w.client.RecentPipelines(ctx, w.projectID, w.cfg.Ref, 8)
	if err != nil {
		return err
	}
	if len(recent) == 0 {
		return fmt.Errorf("no pipelines found")
	}
	latest := recent[0]

	stateKey := fmt.Sprintf("%s@%s@%s", w.cfg.Host, w.cfg.Project, w.cfg.Ref)
	lastSeen := w.store.Get(stateKey)
	triggered := latest.ID > lastSeen
	if triggered {
		if err := w.store.Set(stateKey, latest.ID); err != nil {
			return err
		}
		w.currentID = latest.ID
	} else if w.currentID == 0 {
		w.currentID = latest.ID
	}

	selected := model.Pipeline{ID: w.currentID}
	for _, pipeline := range recent {
		if pipeline.ID == w.currentID {
			selected = pipeline
			break
		}
	}
	// A manually inspected pipeline may have fallen outside the recent list.
	if selected.Status == "" {
		selected, err = w.client.Pipeline(ctx, w.projectID, w.currentID)
		if err != nil {
			return err
		}
	}
	entry, err := w.loadPipeline(ctx, selected)
	if err != nil {
		return err
	}
	w.selectedJobs = append([]model.Job(nil), entry.jobs...)
	w.updateOverview(recent)
	w.emitSnapshot(events, triggered)

	// Hydrate at most one historical pipeline per cycle, not eight recursive
	// trees before displaying the selected pipeline. Completed trees are cached.
	for _, pipeline := range recent {
		if pipeline.ID == w.currentID {
			continue
		}
		cached, ok := w.pipelines[pipeline.ID]
		if ok && pipeline.Status == cached.pipeline.Status && pipeline.UpdatedAt.Equal(cached.pipeline.UpdatedAt) &&
			(settled(cached) || time.Since(cached.refreshedAt) < time.Minute) {
			continue
		}
		if _, err := w.loadPipeline(ctx, pipeline); err != nil {
			return err
		}
		w.updateOverview(recent)
		w.emitSnapshot(events, false)
		break
	}
	return nil
}

func (w *Watcher) loadPipeline(ctx context.Context, pipeline model.Pipeline) (cachedPipeline, error) {
	if cached, ok := w.pipelines[pipeline.ID]; ok && settled(cached) &&
		pipeline.Status == cached.pipeline.Status && pipeline.UpdatedAt.Equal(cached.pipeline.UpdatedAt) {
		return cached, nil
	}
	detail, err := w.client.Pipeline(ctx, w.projectID, pipeline.ID)
	if err != nil {
		return cachedPipeline{}, err
	}
	if title, ok := w.commitTitles[detail.SHA]; ok {
		detail.CommitTitle = title
	} else if detail.SHA != "" {
		title, err := w.client.CommitTitle(ctx, w.projectID, detail.SHA)
		if err != nil {
			return cachedPipeline{}, err
		}
		w.commitTitles[detail.SHA] = title
		detail.CommitTitle = title
	}
	jobs, err := w.client.PipelineJobs(ctx, w.projectID, pipeline.ID)
	if err != nil {
		return cachedPipeline{}, err
	}
	entry := cachedPipeline{pipeline: detail, jobs: jobs, refreshedAt: time.Now()}
	w.pipelines[pipeline.ID] = entry
	return entry, nil
}

func settled(entry cachedPipeline) bool {
	if !terminal(entry.pipeline.Status) {
		return false
	}
	for _, job := range entry.jobs {
		if !terminal(job.Status) && job.Status != "manual" {
			return false
		}
	}
	return true
}

func terminal(status string) bool {
	switch strings.ToLower(status) {
	case "success", "failed", "canceled", "skipped":
		return true
	default:
		return false
	}
}

func (w *Watcher) updateOverview(recent []model.Pipeline) {
	w.overview = make([]model.PipelineSummary, 0, len(recent))
	for _, pipeline := range recent {
		pipeline.CommitTitle = w.commitTitles[pipeline.SHA]
		summary := model.PipelineSummary{Pipeline: pipeline}
		if cached, ok := w.pipelines[pipeline.ID]; ok {
			summary.Stages = gitlab.SummarizeStages(cached.jobs)
		}
		w.overview = append(w.overview, summary)
	}
}

func (w *Watcher) emitSnapshot(events chan<- Event, triggered bool) {
	jobs := append([]model.Job(nil), w.selectedJobs...)
	w.mergeTraceState(jobs)
	snapshot := model.Snapshot{
		Project: w.cfg.Project, Pipelines: w.overview, SelectedPipelineID: w.currentID,
		Pipeline: w.pipelines[w.currentID].pipeline, Stages: gitlab.GroupJobsByStage(jobs),
		UpdatedAt: time.Now(), Triggered: triggered,
	}
	if triggered && w.dispatcher != nil {
		snapshot.ActionText = w.dispatcher.Dispatch(snapshot)
	}
	events <- Event{Snapshot: snapshot}
}

func (w *Watcher) refreshTraces(ctx context.Context, events chan<- Event) error {
	if w.currentID == 0 || w.traceJobID == 0 {
		return nil
	}

	for _, job := range w.selectedJobs {
		if job.ID != w.traceJobID || job.IsBridge || !shouldFetchTrace(job.Status) || w.finishedTraces[job.ID] == job.Status {
			continue
		}

		chunk, offset, replaceExisting, err := w.client.JobTrace(ctx, job.ProjectID, job.ID, w.cachedTraceSize(job.ID))
		if errors.Is(err, gitlab.ErrTraceNotReady) {
			return nil
		}
		if err != nil {
			return err
		}

		w.traceMu.Lock()
		w.traceSize[job.ID] = offset
		if replaceExisting {
			w.traces[job.ID] = chunk
		} else if chunk != "" {
			w.traces[job.ID] += chunk
		}
		w.traceMu.Unlock()
		if terminal(job.Status) {
			w.finishedTraces[job.ID] = job.Status
		}
		w.emitSnapshot(events, false)
		break
	}
	return nil
}

func (w *Watcher) mergeTraceState(jobs []model.Job) {
	for i := range jobs {
		jobs[i].Trace = w.cachedTrace(jobs[i].ID)
		jobs[i].TraceSize = w.cachedTraceSize(jobs[i].ID)
	}
}

func (w *Watcher) cachedTrace(jobID int64) string {
	w.traceMu.Lock()
	defer w.traceMu.Unlock()
	return w.traces[jobID]
}

func (w *Watcher) cachedTraceSize(jobID int64) int64 {
	w.traceMu.Lock()
	defer w.traceMu.Unlock()
	return w.traceSize[jobID]
}

func shouldFetchTrace(status string) bool {
	switch strings.ToLower(status) {
	case "running", "failed", "success", "canceled":
		return true
	default:
		return false
	}
}
