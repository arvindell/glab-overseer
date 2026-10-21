package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/arvindell/glab-overseer/internal/model"
)

type Client struct {
	host            string
	token           string
	http            *http.Client
	requestMu       sync.Mutex
	requestInterval time.Duration
	nextRequest     time.Time
	backoff         time.Duration
}

var ErrTraceNotReady = errors.New("job trace is not available yet")

type RateLimitError struct{ RetryAt time.Time }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("GitLab HTTP 429: waiting until %s before making more requests", e.RetryAt.UTC().Format(time.RFC3339))
}

func NewClient(host, token string, timeout time.Duration, requestInterval time.Duration) *Client {
	return &Client{
		host:            strings.TrimRight(host, "/"),
		token:           token,
		http:            &http.Client{Timeout: timeout},
		requestInterval: requestInterval,
	}
}

func (c *Client) ResolveProjectID(ctx context.Context, projectPath string) (int64, error) {
	var project struct {
		ID int64 `json:"id"`
	}
	if err := c.getJSON(ctx, "/api/v4/projects/"+url.PathEscape(projectPath), &project); err != nil {
		return 0, err
	}
	return project.ID, nil
}

func (c *Client) LatestPipeline(ctx context.Context, projectID int64, ref string) (model.Pipeline, error) {
	query := url.Values{}
	query.Set("per_page", "1")
	query.Set("order_by", "id")
	query.Set("sort", "desc")
	if ref != "" {
		query.Set("ref", ref)
	}

	var pipelines []pipelineResponse
	if err := c.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/pipelines?%s", projectID, query.Encode()), &pipelines); err != nil {
		return model.Pipeline{}, err
	}
	if len(pipelines) == 0 {
		return model.Pipeline{}, fmt.Errorf("no pipelines found")
	}

	return pipelines[0].toModel(), nil
}

func (c *Client) RecentPipelines(ctx context.Context, projectID int64, ref string, limit int) ([]model.Pipeline, error) {
	query := url.Values{}
	query.Set("per_page", fmt.Sprintf("%d", limit))
	query.Set("order_by", "id")
	query.Set("sort", "desc")
	if ref != "" {
		query.Set("ref", ref)
	}

	var pipelines []pipelineResponse
	if err := c.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/pipelines?%s", projectID, query.Encode()), &pipelines); err != nil {
		return nil, err
	}

	out := make([]model.Pipeline, 0, len(pipelines))
	for _, pipeline := range pipelines {
		out = append(out, pipeline.toModel())
	}
	return out, nil
}

func (c *Client) CommitTitle(ctx context.Context, projectID int64, sha string) (string, error) {
	if sha == "" {
		return "", nil
	}
	var commit struct {
		Title string `json:"title"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/repository/commits/%s", projectID, url.PathEscape(sha)), &commit); err != nil {
		return "", err
	}
	return commit.Title, nil
}

func (c *Client) Pipeline(ctx context.Context, projectID, pipelineID int64) (model.Pipeline, error) {
	var pipeline pipelineResponse
	if err := c.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/pipelines/%d", projectID, pipelineID), &pipeline); err != nil {
		return model.Pipeline{}, err
	}
	return pipeline.toModel(), nil
}

func (c *Client) PipelineJobs(ctx context.Context, projectID, pipelineID int64) ([]model.Job, error) {
	type pipelineKey struct{ projectID, pipelineID int64 }
	visited := make(map[pipelineKey]bool)
	var collect func(int64, int64, string) ([]model.Job, error)
	collect = func(projectID, pipelineID int64, pipelinePath string) ([]model.Job, error) {
		key := pipelineKey{projectID, pipelineID}
		if visited[key] {
			return nil, nil
		}
		visited[key] = true
		endpoint := fmt.Sprintf("/api/v4/projects/%d/pipelines/%d", projectID, pipelineID)
		responses, err := getPages[jobResponse](ctx, c, endpoint+"/jobs")
		if err != nil {
			return nil, fmt.Errorf("jobs for pipeline %d in project %d: %w", pipelineID, projectID, err)
		}
		bridges, err := getPages[bridgeResponse](ctx, c, endpoint+"/bridges")
		if err != nil {
			return nil, fmt.Errorf("bridges for pipeline %d in project %d: %w", pipelineID, projectID, err)
		}

		jobs := make([]model.Job, 0, len(responses)+len(bridges))
		for _, response := range responses {
			job := response.toModel()
			job.ProjectID, job.PipelineID, job.PipelinePath = projectID, pipelineID, pipelinePath
			jobs = append(jobs, job)
		}
		for _, bridge := range bridges {
			job := bridge.toModel()
			job.ProjectID, job.PipelineID, job.PipelinePath = projectID, pipelineID, pipelinePath
			job.IsBridge = true
			if bridge.DownstreamPipeline != nil {
				job.DownstreamPipelineID = bridge.DownstreamPipeline.ID
			}
			jobs = append(jobs, job)
			if job.DownstreamPipelineID == 0 {
				continue
			}
			childProjectID := bridge.DownstreamPipeline.ProjectID
			if childProjectID == 0 {
				childProjectID = projectID
			}
			childPath := bridge.Name
			if pipelinePath != "" {
				childPath = pipelinePath + " / " + bridge.Name
			}
			children, err := collect(childProjectID, job.DownstreamPipelineID, childPath)
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, children...)
		}
		return jobs, nil
	}
	return collect(projectID, pipelineID, "")
}

func getPages[T any](ctx context.Context, c *Client, endpoint string) ([]T, error) {
	var results []T
	for page := 1; ; {
		var batch []T
		next, err := c.getJSONPage(ctx, fmt.Sprintf("%s?per_page=100&page=%d", endpoint, page), &batch)
		if err != nil {
			return nil, err
		}
		results = append(results, batch...)
		if next == "" {
			return results, nil
		}
		nextPage, err := strconv.Atoi(next)
		if err != nil || nextPage <= page {
			return nil, fmt.Errorf("invalid next page %q for %s", next, endpoint)
		}
		page = nextPage
	}
}

func (c *Client) JobTrace(ctx context.Context, projectID, jobID int64, offset int64) (string, int64, bool, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v4/projects/%d/jobs/%d/trace", projectID, jobID), nil)
	if err != nil {
		return "", offset, false, err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := c.do(req)
	if err != nil {
		return "", offset, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", offset, false, ErrTraceNotReady
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 {
		return "", offset, false, nil
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", offset, false, fmt.Errorf("gitlab trace request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	contents, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", offset, false, err
	}

	replaceExisting := offset > 0 && resp.StatusCode == http.StatusOK
	newOffset := offset + int64(len(contents))
	if replaceExisting {
		newOffset = int64(len(contents))
	}

	return string(contents), newOffset, replaceExisting, nil
}

func GroupJobsByStage(jobs []model.Job) []model.Stage {
	order := []string{}
	grouped := map[string][]model.Job{}
	firstJobID := map[string]int64{}
	for _, job := range jobs {
		if _, ok := grouped[job.Stage]; !ok {
			order = append(order, job.Stage)
			firstJobID[job.Stage] = job.ID
		} else if job.ID < firstJobID[job.Stage] {
			firstJobID[job.Stage] = job.ID
		}
		grouped[job.Stage] = append(grouped[job.Stage], job)
	}

	for stageName := range grouped {
		sort.SliceStable(grouped[stageName], func(i, j int) bool {
			return grouped[stageName][i].ID < grouped[stageName][j].ID
		})
	}

	sort.SliceStable(order, func(i, j int) bool {
		return firstJobID[order[i]] < firstJobID[order[j]]
	})

	stages := make([]model.Stage, 0, len(order))
	for _, name := range order {
		stages = append(stages, model.Stage{Name: name, Jobs: grouped[name]})
	}
	return stages
}

func SummarizeStages(jobs []model.Job) []model.StageSummary {
	stages := GroupJobsByStage(jobs)
	summaries := make([]model.StageSummary, 0, len(stages))
	for _, stage := range stages {
		summaries = append(summaries, model.StageSummary{
			Name:   stage.Name,
			Status: summarizeStageStatus(stage.Jobs),
		})
	}
	return summaries
}

func summarizeStageStatus(jobs []model.Job) string {
	statusPriority := map[string]int{
		"failed":               0,
		"running":              1,
		"preparing":            1,
		"pending":              2,
		"waiting_for_resource": 2,
		"created":              3,
		"scheduled":            3,
		"manual":               3,
		"canceled":             4,
		"success":              5,
		"skipped":              6,
	}
	bestStatus := "success"
	bestPriority := 99
	for _, job := range jobs {
		status := strings.ToLower(job.Status)
		priority, ok := statusPriority[status]
		if !ok {
			priority = 98
		}
		if priority < bestPriority {
			bestPriority = priority
			bestStatus = status
		}
	}
	return bestStatus
}

func (c *Client) getJSON(ctx context.Context, endpoint string, out any) error {
	_, err := c.getJSONPage(ctx, endpoint, out)
	return err
}

func (c *Client) getJSONPage(ctx context.Context, endpoint string, out any) (string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gitlab request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return "", fmt.Errorf("decode gitlab response: %w", err)
	}

	return resp.Header.Get("X-Next-Page"), nil
}

func (c *Client) newRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	requestURL, err := url.Parse(c.host + endpoint)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// All metadata and trace requests share a budget and server-directed cooldown.
func (c *Client) do(req *http.Request) (*http.Response, error) {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	if delay := time.Until(c.nextRequest); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	c.nextRequest = time.Now().Add(c.requestInterval)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		if c.backoff == 0 {
			c.backoff = time.Minute
		} else {
			c.backoff = min(c.backoff*2, 15*time.Minute)
		}
		c.nextRequest = maxTime(c.nextRequest, time.Now().Add(c.backoff))
	} else if resp.StatusCode < 400 {
		c.backoff = 0
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.Header.Get("RateLimit-Remaining") == "0" {
		if seconds, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil && seconds >= 0 {
			c.nextRequest = maxTime(c.nextRequest, time.Now().Add(time.Duration(seconds*float64(time.Second))))
		} else if reset, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			c.nextRequest = maxTime(c.nextRequest, reset)
		}
		if reset, err := strconv.ParseInt(resp.Header.Get("RateLimit-Reset"), 10, 64); err == nil {
			c.nextRequest = maxTime(c.nextRequest, time.Unix(reset, 0))
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return nil, &RateLimitError{RetryAt: c.nextRequest}
	}
	return resp, nil
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

type pipelineResponse struct {
	ID        int64      `json:"id"`
	IID       int64      `json:"iid"`
	Status    string     `json:"status"`
	WebURL    string     `json:"web_url"`
	Source    string     `json:"source"`
	Ref       string     `json:"ref"`
	SHA       string     `json:"sha"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	User      userStruct `json:"user"`
}

func (p pipelineResponse) toModel() model.Pipeline {
	return model.Pipeline{
		ID:         p.ID,
		IID:        p.IID,
		Status:     p.Status,
		WebURL:     p.WebURL,
		Source:     p.Source,
		Ref:        p.Ref,
		SHA:        p.SHA,
		CreatedAt:  p.CreatedAt,
		UpdatedAt:  p.UpdatedAt,
		UserName:   p.User.Name,
		UserHandle: p.User.Username,
	}
}

type jobResponse struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Stage      string     `json:"stage"`
	Status     string     `json:"status"`
	WebURL     string     `json:"web_url"`
	Duration   float64    `json:"duration"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

type bridgeResponse struct {
	jobResponse
	DownstreamPipeline *struct {
		ID        int64 `json:"id"`
		ProjectID int64 `json:"project_id"`
	} `json:"downstream_pipeline"`
}

func (j jobResponse) toModel() model.Job {
	return model.Job{
		ID:         j.ID,
		Name:       j.Name,
		Stage:      j.Stage,
		Status:     j.Status,
		WebURL:     j.WebURL,
		Duration:   time.Duration(j.Duration * float64(time.Second)),
		StartedAt:  j.StartedAt,
		FinishedAt: j.FinishedAt,
	}
}

type userStruct struct {
	Name     string `json:"name"`
	Username string `json:"username"`
}
