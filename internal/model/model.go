package model

import "time"

type Pipeline struct {
	ID         int64
	IID        int64
	Status     string
	WebURL     string
	Source     string
	Ref        string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	UserName   string
	UserHandle string
}

type Job struct {
	ID         int64
	Name       string
	Stage      string
	Status     string
	WebURL     string
	Duration   time.Duration
	StartedAt  *time.Time
	FinishedAt *time.Time
	Trace      string
	TraceSize  int64
}

type Stage struct {
	Name string
	Jobs []Job
}

type Snapshot struct {
	Project    string
	Pipeline   Pipeline
	Stages     []Stage
	UpdatedAt  time.Time
	Triggered  bool
	LastError  string
	ActionText string
}
