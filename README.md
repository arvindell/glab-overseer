# glab-overseer

`glab-overseer` is a terminal watcher for GitLab pipelines built with Go and pure GitLab HTTP API calls.

It polls for new pipelines, deduplicates triggers, tails job logs, and renders a terminal UI inspired by `glab ci view`.

## Why

GitLab does not provide a streaming API for pipeline updates, and `glab ci view`
requires an interactive shell session. `glab-overseer` focuses on a different
use case: an always-on pipeline watcher that can stay open in a terminal,
trigger actions for newly detected pipelines, and show stage/job progress with
live logs.

## Features

- Detect new pipelines with polling only
- Trigger async actions on new pipelines: `none`, `log`, `open`, `notify`
- Fetch pipeline jobs and stream job traces via GitLab REST API
- Render a stage-by-stage TUI with a log pane
- Persist the last seen pipeline ID to avoid duplicate triggers across restarts
- Watch a specific branch/ref when needed
- Keep actions non-blocking while the UI continues to update

## Requirements

- Go `1.26+`
- A GitLab Personal Access Token with `read_api`
- Access to the project you want to monitor

## Setup

1. Copy `.env.example` to `.env`.
2. Run `go mod tidy`.
3. Start the app:

```bash
go run . --project your-group/your-project
```

Or install it directly:

```bash
go install github.com/arvindell/glab-overseer@latest
```

## Releases

Tagged releases publish downloadable binaries to GitHub Releases for:

- Linux `amd64`
- Linux `arm64`
- Linux `armv7`
- macOS `amd64`
- macOS `arm64`

To publish a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

That triggers the release workflow and uploads archives automatically.

For Raspberry Pi:

- 64-bit Raspberry Pi OS: download `linux_arm64`
- 32-bit Raspberry Pi OS: download `linux_armv7`

## Environment Variables

- `GITLAB_TOKEN`: personal access token with `read_api`
- `GITLAB_HOST`: GitLab host, defaults to `https://gitlab.com`
- `GITLAB_PROJECT`: project path, e.g. `group/project`
- `GITLAB_REF`: optional branch filter
- `OVERSEER_POLL_INTERVAL`: pipeline poll interval, defaults to `15s`
- `OVERSEER_TRACE_INTERVAL`: trace poll interval, defaults to `3s`
- `OVERSEER_ACTION`: `none`, `log`, `open`, or `notify`
- `OVERSEER_STATE_FILE`: path to the persisted state file

## CLI Flags

- `--project`
- `--host`
- `--token`
- `--ref`
- `--interval`
- `--trace-interval`
- `--action`
- `--state-file`

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

## Raspberry Pi Install

Example for a 64-bit Raspberry Pi:

```bash
curl -L https://github.com/arvindell/glab-overseer/releases/download/v0.1.0/glab-overseer_v0.1.0_linux_arm64.tar.gz -o glab-overseer.tar.gz
tar -xzf glab-overseer.tar.gz
chmod +x glab-overseer
sudo mv glab-overseer /usr/local/bin/
glab-overseer --version
```

Example for a 32-bit Raspberry Pi:

```bash
curl -L https://github.com/arvindell/glab-overseer/releases/download/v0.1.0/glab-overseer_v0.1.0_linux_armv7.tar.gz -o glab-overseer.tar.gz
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

## Roadmap

- richer connector-line graph layout
- better trace tailing with byte-range resume
- multi-project watch mode
- job actions such as retry and manual-play
- stronger failure-focused notifications and summaries

## Contributing

Contributions are welcome. See [`CONTRIBUTING.md`](./CONTRIBUTING.md).

## Security

If you discover a security issue, please follow [`SECURITY.md`](./SECURITY.md)
instead of opening a public issue.

## License

MIT
