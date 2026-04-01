# Contributing

Thanks for contributing to `glab-overseer`.

## Before You Start

- Search for an existing issue or pull request before starting work.
- Open an issue for large changes so the approach can be aligned early.
- Keep changes focused and easy to review.

## Local Development

1. Install Go `1.26+`.
2. Copy `.env.example` to `.env` and fill in your GitLab settings.
3. Install dependencies:

```bash
go mod tidy
```

4. Run the app:

```bash
go run . --project your-group/your-project
```

## Checks

Run these before opening a pull request:

```bash
gofmt -w .
go test ./...
go vet ./...
go build ./...
```

## Pull Requests

- Explain the problem and the approach.
- Include screenshots or terminal recordings for UI changes when possible.
- Update documentation when behavior or configuration changes.
- Avoid mixing unrelated refactors with feature work.

## Design Notes

Project goals:

- pure GitLab HTTP API usage
- polling-based updates only
- low-noise, useful terminal UI
- safe deduplication and non-blocking side effects

When in doubt, prefer a smaller, composable change over a broad rewrite.
