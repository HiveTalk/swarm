# Swarm verification

- Use a separate Git worktree when another process is working in the checkout. Concurrent branch switches invalidate build and test results.
- Keep `.go-version`, the `go.mod` toolchain, and the Dockerfile Go builder version synchronized; CI enforces this.
- The govulncheck action defaults to installing stable Go even after setup-go has run. Set its `go-version-input` to an empty string and `go-version-file` to `.go-version` so the scan uses the build toolchain.
- Local checks: `test -z "$(gofmt -l .)"`, `python3 -m unittest discover -s test/workflows -v`, `go vet ./...`, `go test -race ./...`, and `go build ./...`.
- Go E2E tests skip network checks unless `E2E_RELAY_URL` is set. Run the isolated container setup in `.github/workflows/ci.yml`, then `go test -count=1 -v ./test/e2e` and `npm run test:e2e` with its generated test identity environment.
- Required main-branch checks are defined in the active repository ruleset, not classic branch protection. Do not weaken the ruleset or auto-merge eligibility policy.
- Production promotion is manual and requires the production environment approval. Successful `Build and stage` runs do not deploy production.
