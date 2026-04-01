# glab-overseer

`glab-overseer` is a terminal watcher for GitLab pipelines.

It uses the GitLab REST API directly, polls for new pipelines, triggers actions
when a new pipeline appears, and shows live stage/job progress with job logs in
a terminal UI inspired by `glab ci view`.

## Features

- pure HTTP calls to GitLab, no `glab` and no SDKs
- polling-based pipeline detection
- deduped new-pipeline triggers across restarts
- async actions: `none`, `log`, `open`, `notify`
- stage/job TUI with live trace polling
- release binaries for macOS and Linux

## Install

From source:

```bash
go install github.com/arvindell/glab-overseer@latest
```

Or download a binary from GitHub Releases.

## Config

Copy `.env.example` to `.env` and fill in:

```env
GITLAB_HOST=https://gitlab.com
GITLAB_PROJECT=group/project
GITLAB_TOKEN=your_gitlab_pat
GITLAB_REF=
OVERSEER_POLL_INTERVAL=15s
OVERSEER_TRACE_INTERVAL=3s
OVERSEER_ACTION=notify
```

Required:

- `GITLAB_PROJECT`
- `GITLAB_TOKEN`

The token should have `read_api` access.

## Run

```bash
glab-overseer --project group/project
```

Or from the repo:

```bash
go run . --project group/project
```

Useful flags:

- `--ref`
- `--interval`
- `--trace-interval`
- `--action`
- `--state-file`

## Keybindings

- `left` / `right`: change stage
- `up` / `down`: change job
- `g` / `G`: top / bottom of logs
- `q`: quit

## Release

Create and push a version tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

That triggers the GitHub release workflow and uploads binaries automatically.

## Development

```bash
gofmt -w .
go test ./...
go vet ./...
go build ./...
```

## Project Docs

- [`CONTRIBUTING.md`](./CONTRIBUTING.md)
- [`SECURITY.md`](./SECURITY.md)
- [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md)
- [`CHANGELOG.md`](./CHANGELOG.md)
- [`LICENSE`](./LICENSE)
