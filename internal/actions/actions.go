package actions

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"

	open "github.com/skratchdot/open-golang/open"

	"github.com/arvindell/glab-overseer/internal/model"
)

type Action string

const (
	ActionNone   Action = "none"
	ActionLog    Action = "log"
	ActionOpen   Action = "open"
	ActionNotify Action = "notify"
)

func ParseAction(value string) (Action, error) {
	action := Action(strings.ToLower(strings.TrimSpace(value)))
	switch action {
	case ActionNone, ActionLog, ActionOpen, ActionNotify:
		return action, nil
	default:
		return "", fmt.Errorf("invalid action %q", value)
	}
}

type Dispatcher struct {
	action Action
	jobs   chan model.Snapshot
	seen   sync.Map
	wg     sync.WaitGroup
}

func NewDispatcher(action Action, workers int) *Dispatcher {
	d := &Dispatcher{
		action: action,
		jobs:   make(chan model.Snapshot, 32),
	}

	for range workers {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			for snapshot := range d.jobs {
				d.run(snapshot)
			}
		}()
	}

	return d
}

func (d *Dispatcher) Dispatch(snapshot model.Snapshot) string {
	key := fmt.Sprintf("%s:%d", snapshot.Project, snapshot.Pipeline.ID)
	if _, loaded := d.seen.LoadOrStore(key, true); loaded {
		return "deduped"
	}

	select {
	case d.jobs <- snapshot:
		return string(d.action)
	default:
		go d.run(snapshot)
		return string(d.action)
	}
}

func (d *Dispatcher) Close() {
	close(d.jobs)
	d.wg.Wait()
}

func (d *Dispatcher) run(snapshot model.Snapshot) {
	switch d.action {
	case ActionNone:
		return
	case ActionLog:
		log.Printf("new pipeline detected: #%d %s (%s)", snapshot.Pipeline.ID, snapshot.Pipeline.Status, snapshot.Pipeline.WebURL)
	case ActionOpen:
		if err := open.Run(snapshot.Pipeline.WebURL); err != nil {
			log.Printf("open pipeline url: %v", err)
		}
	case ActionNotify:
		title := fmt.Sprintf("Pipeline #%d", snapshot.Pipeline.ID)
		body := fmt.Sprintf("%s triggered by %s", snapshot.Pipeline.Status, snapshot.Pipeline.UserName)
		cmd := exec.Command("osascript", "-e", fmt.Sprintf(`display notification %q with title %q`, body, title))
		if err := cmd.Run(); err != nil {
			log.Printf("send notification: %v", err)
		}
	}
}
