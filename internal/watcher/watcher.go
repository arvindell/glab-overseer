package watcher

import (
	"context"
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
}

type Watcher struct {
	client     *gitlab.Client
	store      *state.Store
	dispatcher *actions.Dispatcher
	cfg        config.Config
	commands   <-chan Command

	projectID int64
	traceMu   sync.Mutex
	traceSize map[int64]int64
	traces    map[int64]string
	currentID int64
	overview  []model.PipelineSummary
}

func New(client *gitlab.Client, store *state.Store, dispatcher *actions.Dispatcher, cfg config.Config, commands <-chan Command) *Watcher {
	return &Watcher{
		client:     client,
		store:      store,
		dispatcher: dispatcher,
		cfg:        cfg,
		commands:   commands,
		traceSize:  map[int64]int64{},
		traces:     map[int64]string{},
	}
}

func (w *Watcher) Run(ctx context.Context, events chan<- Event) {
	projectID, err := w.client.ResolveProjectID(ctx, w.cfg.Project)
	if err != nil {
		events <- Event{Err: err}
		return
	}
	w.projectID = projectID

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
			if cmd.SelectPipelineID != 0 {
				w.currentID = cmd.SelectPipelineID
				if err := w.refresh(ctx, events); err != nil {
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
	latest, err := w.client.LatestPipeline(ctx, w.projectID, w.cfg.Ref)
	if err != nil {
		return err
	}

	stateKey := fmt.Sprintf("%s@%s", w.cfg.Host, w.cfg.Project)
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

	overviewPipelines, err := w.client.RecentPipelines(ctx, w.projectID, w.cfg.Ref, 8)
	if err != nil {
		return err
	}

	overview := make([]model.PipelineSummary, 0, len(overviewPipelines))
	for _, pipeline := range overviewPipelines {
		detailedPipeline, err := w.client.Pipeline(ctx, w.projectID, pipeline.ID)
		if err == nil {
			pipeline = detailedPipeline
		}
		commitTitle, err := w.client.CommitTitle(ctx, w.projectID, pipeline.SHA)
		if err == nil {
			pipeline.CommitTitle = commitTitle
		}
		jobs, err := w.client.PipelineJobs(ctx, w.projectID, pipeline.ID)
		if err != nil {
			continue
		}
		overview = append(overview, model.PipelineSummary{
			Pipeline: pipeline,
			Stages:   gitlab.SummarizeStages(jobs),
		})
	}
	w.overview = overview

	pipeline, err := w.client.Pipeline(ctx, w.projectID, w.currentID)
	if err != nil {
		return err
	}
	jobs, err := w.client.PipelineJobs(ctx, w.projectID, w.currentID)
	if err != nil {
		return err
	}

	w.mergeTraceState(jobs)
	snapshot := model.Snapshot{
		Project:            w.cfg.Project,
		Pipelines:          overview,
		SelectedPipelineID: w.currentID,
		Pipeline:           pipeline,
		Stages:             gitlab.GroupJobsByStage(jobs),
		UpdatedAt:          time.Now(),
		Triggered:          triggered,
	}

	if triggered {
		snapshot.ActionText = w.dispatcher.Dispatch(snapshot)
	}

	events <- Event{Snapshot: snapshot}
	return nil
}

func (w *Watcher) refreshTraces(ctx context.Context, events chan<- Event) error {
	if w.currentID == 0 {
		return nil
	}

	pipeline, err := w.client.Pipeline(ctx, w.projectID, w.currentID)
	if err != nil {
		return err
	}
	jobs, err := w.client.PipelineJobs(ctx, w.projectID, w.currentID)
	if err != nil {
		return err
	}

	fetchOrder := make([]int, 0, len(jobs))
	for i := range jobs {
		fetchOrder = append(fetchOrder, i)
	}

	for _, i := range fetchOrder {
		if !shouldFetchTrace(jobs[i].Status) {
			jobs[i].Trace = w.cachedTrace(jobs[i].ID)
			jobs[i].TraceSize = w.cachedTraceSize(jobs[i].ID)
			continue
		}

		chunk, offset, replaceExisting, err := w.client.JobTrace(ctx, w.projectID, jobs[i].ID, w.cachedTraceSize(jobs[i].ID))
		if err != nil {
			jobs[i].Trace = w.cachedTrace(jobs[i].ID)
			continue
		}

		w.traceMu.Lock()
		w.traceSize[jobs[i].ID] = offset
		if replaceExisting {
			w.traces[jobs[i].ID] = chunk
		} else if chunk != "" {
			w.traces[jobs[i].ID] += chunk
		}
		jobs[i].Trace = w.traces[jobs[i].ID]
		jobs[i].TraceSize = offset
		w.traceMu.Unlock()
	}

	events <- Event{Snapshot: model.Snapshot{
		Project:            w.cfg.Project,
		Pipelines:          w.overview,
		SelectedPipelineID: w.currentID,
		Pipeline:           pipeline,
		Stages:             gitlab.GroupJobsByStage(jobs),
		UpdatedAt:          time.Now(),
	}}

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
