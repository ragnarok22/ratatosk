# Contributing to Ratatosk

Thanks for contributing. Ratatosk is still in alpha, so focused changes, clear reproduction steps, and strong tests matter more than volume.

## Before You Open an Issue or PR

- Search existing issues and pull requests first to avoid duplicate work.
- Use the [Security Policy](./SECURITY.md) for vulnerabilities or other sensitive reports. Do not open public issues for undisclosed security problems.
- Follow the [Code of Conduct](./CODE_OF_CONDUCT.md) in all project spaces.

## Development Setup

Prerequisites:

- Go 1.26+
- Node.js 20+
- pnpm

Install dashboard dependencies if you plan to work on the frontend:

```sh
cd cmd/server/dashboard
pnpm install
```

## Repository Layout

- `cmd/server` contains the relay server entrypoint.
- `cmd/cli` contains the local client entrypoint.
- `internal/protocol` contains request and response types shared by the server and client.
- `internal/tunnel` contains multiplexing and session registry logic.
- `internal/inspector` contains the local traffic inspection UI and proxy helpers.
- `cmd/server/dashboard` contains the React + Vite dashboard.

## Local Development

Start the server:

```sh
make dev-server
```

Start the CLI client:

```sh
make dev-cli
```

Start the dashboard in development mode:

```sh
make dev-dashboard
```

## Coding Guidelines

- Keep `cmd/` packages thin. Put reusable logic in `internal/` whenever possible.
- Prefer small, targeted changes over broad refactors unless the refactor is the change.
- Treat concurrency as a first-class concern. Shared state must be protected with the right synchronization primitives.
- Do not suppress lint warnings just to get clean output. Fix the underlying problem.
- Update documentation when behavior, flags, workflows, or architecture change.

## Testing Expectations

- Every behavior change should come with tests or updated tests.
- New packages under `cmd/` or `internal/` should include at least basic `_test.go` coverage.
- For connection and multiplexing tests, prefer `net.Pipe()` over real network listeners when possible.
- Run the race detector before submitting changes that touch shared state, networking, or goroutine coordination.

For Go changes, run:

```sh
make format
make lint
make test
make test-race
make coverage
```

For dashboard changes, run from `cmd/server/dashboard`:

```sh
pnpm run check
pnpm run typecheck
pnpm run build
```

## Pull Request Guidelines

- Keep pull requests focused on one problem or feature.
- Explain the motivation, approach, and user-visible impact in the PR description.
- Include the verification steps you ran.
- Add screenshots or short recordings for dashboard or UI changes.
- Call out breaking changes, protocol changes, or changes that affect deployment or local setup.

## Reporting Bugs

Good bug reports include:

- What you expected to happen
- What actually happened
- Clear reproduction steps
- Relevant logs, error messages, or packet traces
- Your OS, architecture, and any network or proxy details that matter
- The branch name or commit SHA you tested

## Feature Proposals

When proposing a feature, describe the use case first. A concrete problem statement is more useful than a broad implementation sketch.
