FROM mcr.microsoft.com/devcontainers/go:2-1.25-trixie

ENV DOCKER_HOST=unix:///var/run/docker.sock

RUN apt-get update \
    && apt-get install -y --no-install-recommends bubblewrap ca-certificates curl git make docker-cli docker-compose \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspaces/sekai-master-api
