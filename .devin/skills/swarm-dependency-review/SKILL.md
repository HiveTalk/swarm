---
name: swarm-dependency-review
description: Review a Swarm dependency pull request for merge eligibility
argument-hint: "[pull-request-number]"
allowed-tools:
  - read
  - grep
  - glob
  - exec
triggers:
  - user
  - model
---

Review the requested dependency pull request without merging or pushing it.

1. Confirm the pull request actor is Dependabot and inspect every changed file.
2. Record the dependency names, old and new versions, update type, and whether the update addresses a published vulnerability.
3. Treat `github.com/fiatjaf/khatru`, `github.com/fiatjaf/eventstore`, `github.com/nbd-wtf/go-nostr`, database backends, authentication libraries, cryptography libraries, and major container changes as sensitive.
4. Read upstream release notes for direct or sensitive dependencies. Identify breaking API, protocol, persistence, authentication, or configuration changes.
5. Verify that no workflow, application source, generated asset, or unrelated manifest changes are hidden in the pull request.
6. Inspect required GitHub checks and failure logs.
7. Check out the pull request only when the worktree is clean, then run formatting verification, `go vet ./...`, `go test -race ./...`, `go build ./...`, and the container E2E suite used by CI.
8. Report one decision: eligible for auto-merge, eligible for human merge, or blocked.
9. Include concrete evidence, remaining risks, and the exact failed check for blocked reviews.

Never merge, push, dismiss a security alert, weaken a required check, or change repository settings from this skill.
