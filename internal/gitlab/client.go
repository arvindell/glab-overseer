package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/arvindell/glab-overseer/internal/model"
)

type Client struct {
	host  string
	token string
	http  *http.Client
}

func NewClient(host, token string, timeout time.Duration) *Client {
	return &Client{
		host:  strings.TrimRight(host, "/"),
		token: token,
		http:  &http.Client{Timeout: timeout},
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

func (c *Client) Pipeline(ctx context.Context, projectID, pipelineID int64) (model.Pipeline, error) {
	var pipeline pipelineResponse
	if err := c.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/pipelines/%d", projectID, pipelineID), &pipeline); err != nil {
		return model.Pipeline{}, err
	}
	return pipeline.toModel(), nil
}

func (c *Client) PipelineJobs(ctx context.Context, projectID, pipelineID int64) ([]model.Job, error) {
	var responses []jobResponse
	if err := c.getJSON(ctx, fmt.Sprintf("/api/v4/projects/%d/pipelines/%d/jobs?per_page=100", projectID, pipelineID), &responses); err != nil {
		return nil, err
	}

	jobs := make([]model.Job, 0, len(responses))
	for _, job := range responses {
		jobs = append(jobs, job.toModel())
	}

	return jobs, nil
}

func (c *Client) JobTrace(ctx context.Context, projectID, jobID int64, offset int64) (string, int64, error) {
	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v4/projects/%d/jobs/%d/trace", projectID, jobID), nil)
	if err != nil {
		return "", offset, err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", offset, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", offset, nil
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", offset, fmt.Errorf("gitlab trace request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	contents, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", offset, err
	}

	return string(contents), offset + int64(len(contents)), nil
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

func (c *Client) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gitlab request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode gitlab response: %w", err)
	}

	return nil
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

type pipelineResponse struct {
	ID        int64      `json:"id"`
	IID       int64      `json:"iid"`
	Status    string     `json:"status"`
	WebURL    string     `json:"web_url"`
	Source    string     `json:"source"`
	Ref       string     `json:"ref"`
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
