# glab-overseer

`glab-overseer` is a terminal watcher for GitLab pipelines built with Go and pure GitLab HTTP API calls.

It polls for new pipelines, deduplicates triggers, tails job logs, and renders a terminal UI inspired by `glab ci view`.

## Features

- Detect new pipelines with polling only
- Trigger async actions on new pipelines: `none`, `log`, `open`, `notify`
- Fetch pipeline jobs and stream job traces via GitLab REST API
- Render a stage-by-stage TUI with a log pane
- Persist the last seen pipeline ID to avoid duplicate triggers across restarts

## Setup

1. Copy values into `.env`.
2. Run `go mod tidy`.
3. Start the app:

```bash
go run . --project your-group/your-project
```

## Environment Variables

- `GITLAB_TOKEN`: personal access token with `read_api`
- `GITLAB_HOST`: GitLab host, defaults to `https://gitlab.com`
- `GITLAB_PROJECT`: project path, e.g. `group/project`
- `GITLAB_REF`: optional branch filter
- `OVERSEER_POLL_INTERVAL`: pipeline poll interval, defaults to `15s`
- `OVERSEER_TRACE_INTERVAL`: trace poll interval, defaults to `3s`
- `OVERSEER_ACTION`: `none`, `log`, `open`, or `notify`
- `OVERSEER_STATE_FILE`: path to the persisted state file

## Keybindings

- `left` / `right`: move across stages
- `up` / `down`: move across jobs in the selected stage
- `g` / `G`: jump to top/bottom of the log pane
- `q`: quit

## Example

```bash
go run . \
  --project quentli/quentli \
  --interval 15s \
  --trace-interval 3s \
  --action notify
```
