# Release Process

How `sekai-master-api` container images and GitHub Releases are produced. The
pipeline is defined in `.github/workflows/release.yml`, runs on the trusted
organization self-hosted runner (`rarecloud`), and is triggered only by `v*`
tag pushes. Registry credentials on that runner are allowed for trusted events
(tag push qualifies) and must never run on `pull_request` events; see
`docs/cross-repository/trusted-self-hosted-runner.md` in the workspace root.

## Prerequisites

- Secrets `REGISTRY_USERNAME` and `REGISTRY_PASSWORD` (credentials for
  `docker.dnaroma.eu`) configured at repository or organization level. No other
  secrets are required; release creation uses the workflow `GITHUB_TOKEN`.
- The application Dockerfile at `deploy/compose/app/Dockerfile` accepts the
  build args `VERSION`, `COMMIT`, and `BUILD_DATE` and injects them into
  `internal/version` via `-ldflags`.
- Push access to create tags and merge to the default branch.

## Two-step release flow

1. **Release-prep PR**: run

   ```sh
   mise run release-prep 1.2.3
   ```

   This validates the version, cuts the `release/vX.Y.Z` branch from
   `origin/main`, bumps `deploy/helm/sekai-master-api/Chart.yaml`
   (`version` and `appVersion`), pushes the branch, and opens the Release PR.
   Merge after review with CI green. (Equivalent manual path: update the chart
   file yourself and open a PR titled `chore(release): vX.Y.Z`.)
2. **Tag**: from the merged default branch, push an annotated tag:

   ```sh
   git tag -a vX.Y.Z -m "Release vX.Y.Z"
   git push origin vX.Y.Z
   ```

   Pushing the tag triggers `release.yml`; no further manual steps.

Never reuse or re-point a published version tag: images already distributed
under a version must stay immutable.

## What release.yml does

1. Validates the tag matches `^v[0-9]+\.[0-9]+\.[0-9]+$` and that the chart's
   `appVersion` equals the tag without the `v` prefix; fails fast otherwise.
2. Runs `mise run lint` and `mise run test`.
3. Builds the `linux/amd64` image from `deploy/compose/app/Dockerfile` with
   build args `VERSION=<X.Y.Z>`, `COMMIT=<full SHA>`, and
   `BUILD_DATE=<RFC3339 UTC>`.
4. Pushes to `docker.dnaroma.eu/sekai-world/sekai-master-api` and records the
   pushed digest in the workflow step summary.
5. Creates a GitHub Release for the tag with auto-generated notes plus the
   image reference, digest, and a rollback hint.

## Image tagging scheme

| Tag | Meaning |
| --- | --- |
| `X.Y.Z` | Immutable release tag, matches the chart `appVersion`. |
| `latest` | Moving alias for the most recent release; do not deploy against it. |
| `sha-<short>` | Alias of the exact source commit the image was built from. |

## Digest-first consumption

Deployments should pin images as `tag@digest` so what runs is exactly what the
release pipeline pushed. The Helm chart renders
`image.repository:image.tag`, so set:

```yaml
image:
  repository: docker.dnaroma.eu/sekai-world/sekai-master-api
  tag: "1.2.3@sha256:<digest>"
```

Resolve the digest from the GitHub Release notes, the workflow step summary,
or `docker buildx imagetools inspect
docker.dnaroma.eu/sekai-world/sekai-master-api:X.Y.Z`.

## Rollback by digest

1. Find the previous good digest (prior release notes or
   `docker buildx imagetools inspect ...:<previous-version>`).
2. Update the deployment values to
   `tag: "<previous-version>@<previous-digest>"` and run `helm upgrade`.
3. Verify the running pods report the expected version before treating the
   rollback as complete.
