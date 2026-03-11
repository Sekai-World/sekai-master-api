FROM mcr.microsoft.com/devcontainers/go:2-1.25-trixie

ENV DOCKER_HOST=unix:///var/run/docker.sock

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git make docker.io docker-compose-v2 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspaces/sekai-master-api
