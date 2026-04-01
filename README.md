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
- release binaries for macOS and Linux, including Raspberry Pi

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

Raspberry Pi assets:

- 64-bit Raspberry Pi OS: `linux_arm64`
- 32-bit Raspberry Pi OS: `linux_armv7`

Example install on a 64-bit Raspberry Pi:

```bash
curl -L https://github.com/arvindell/glab-overseer/releases/download/v0.1.0/glab-overseer_v0.1.0_linux_arm64.tar.gz -o glab-overseer.tar.gz
tar -xzf glab-overseer.tar.gz
chmod +x glab-overseer
sudo mv glab-overseer /usr/local/bin/
glab-overseer --version
```

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
