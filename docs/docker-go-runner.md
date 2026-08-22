# Docker Go Runner

## Alpine Go image PATH pitfall

`golang:1.26.5-alpine3.23` contains the Go toolchain at
`/usr/local/go/bin/go` and `/usr/local/go/bin/gofmt`. A missing `go` command
in a container is therefore not, by itself, evidence that the image tag is
wrong or incomplete.

When passing a compound command to an Alpine/BusyBox shell, avoid `sh -lc`.
Its login-shell mode can reset `PATH` and omit `/usr/local/go/bin`, causing
`go` and `gofmt` to be unavailable. Prefer a non-login shell:

```sh
sh -c 'go test ./...'
```

For an independently constructed `docker run` command where PATH propagation
is uncertain, invoke the binaries by absolute path:

```sh
/usr/local/go/bin/go test -buildvcs=false ./internal/storage -count=1
/usr/local/go/bin/gofmt -l ./internal/storage
```

`scripts/docker-go.sh` explicitly prepends `/usr/local/go/bin` before executing
its supplied command. Keep that setup when changing the runner, and verify the
final command path rather than changing the image tag solely because a nested
login shell cannot find Go.

Source: verified against `golang:1.26.5-alpine3.23` while validating Phase 2a
on 2026-08-04; runner: `scripts/docker-go.sh`.
