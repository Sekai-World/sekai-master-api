FROM mcr.microsoft.com/devcontainers/go:2-1.25-trixie

ENV CONTAINER_HOST=unix:///var/run/podman-host.sock

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git make podman podman-compose \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspaces/sekai-master-api
