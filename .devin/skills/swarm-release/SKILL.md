---
name: swarm-release
description: Verify and promote a staging-tested Swarm image to production
argument-hint: "[image-digest]"
allowed-tools:
  - read
  - grep
  - glob
triggers:
  - user
---

Prepare a production promotion for the supplied image digest.

1. Require an immutable `sha256:` digest. Do not accept `latest`, a branch name, or a mutable tag.
2. Find the successful `Build and stage` workflow run that produced the digest.
3. Confirm CI, security scanning, image build, staging deployment, and staging E2E checks all succeeded for the same commit and digest.
4. Confirm the production GitHub Environment has a required reviewer and the deployment is serialized.
5. Confirm the VPS backup command exists, the latest backup completed successfully, and the previous production image can still be pulled or is present locally.
6. Summarize the code and dependency changes since the currently deployed production commit.
7. Present the digest, commit, staging evidence, backup evidence, expected health checks, and rollback image to the operator.
8. Ask for explicit confirmation before dispatching the `Promote production` workflow.
9. Monitor the workflow and external smoke checks after approval. If verification fails, report the automatic rollback result immediately.

Never bypass GitHub Environment approval, rewrite history, push directly to `main`, expose deployment secrets, dismiss failed checks, or restore production data automatically.
