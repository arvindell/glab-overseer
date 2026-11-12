# Changelog

All notable changes to this project will be documented in this file.

The format is inspired by Keep a Changelog and this project aims to follow
Semantic Versioning once public releases begin.

## Unreleased

## 1.2.0 — 2026-11-12

- Publish the newest pipeline identity and status before loading its job tree, even when job requests fail.
- Ignore UI selections queued from an older pipeline view so they cannot restore an obsolete pipeline.
- Pace metadata and log polling within a shared request budget and honor GitLab rate-limit cooldowns.
- Show parent/orchestration jobs in a compact selectable strip, leaving stage columns for workloads.
- Focus running workloads and fetch their first logs without waiting for preloaded traces.
- Bound tall job lists to the terminal and keep child provenance in the log heading.
- Show recent pipelines before their job details finish loading.
- Keep the overview within the terminal height and scroll with pipeline selection.
- Do not expose the configured GitLab token in command-line help.
- Recover automatically if GitLab is unreachable when the monitor starts.

## 1.1.0 — 2026-10-19

- Show trigger jobs and all jobs from nested child/downstream pipelines, including paginated results.
- Identify child jobs and fetch their logs from the correct project.
- Explain trigger-job status and keep stage navigation within the terminal width.

## Earlier releases

- Initial private bootstrap
- GitLab HTTP watcher
- Pipeline stage TUI
- Job trace polling and log pane
