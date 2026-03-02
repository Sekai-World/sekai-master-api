FROM mcr.microsoft.com/devcontainers/go:2-1.25-trixie

ENV DOCKER_HOST=unix:///var/run/docker-host.sock
ENV CONTAINER_HOST=unix:///var/run/docker-host.sock

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl gnupg git make podman \
    && install -m 0755 -d /etc/apt/keyrings \
    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \
    && chmod a+r /etc/apt/keyrings/docker.asc \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian trixie stable" > /etc/apt/sources.list.d/docker.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends docker-ce-cli docker-compose-plugin \
    && apt-get purge -y --auto-remove gnupg \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspaces/sekai-master-api
