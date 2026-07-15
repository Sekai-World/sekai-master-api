FROM golang:1.26.5-alpine3.23 AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
	go mod download

COPY cmd/ cmd/
COPY internal/ internal/

RUN --mount=type=cache,target=/go/pkg/mod \
	--mount=type=cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=linux GOARCH=$(go env GOARCH) \
	go build -buildvcs=false -o /out/sekai-master-api ./cmd/api

COPY .env.development .env.example .env.test /tmp/env/

RUN mkdir -p /out/env && \
	find /tmp/env -maxdepth 1 -type f -name '.env*' -exec cp {} /out/env/ \;

FROM alpine:3.24 AS runtime-assets

RUN apk add --no-cache ca-certificates tzdata

FROM busybox:1.37.0-musl

WORKDIR /app

COPY --from=runtime-assets /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=runtime-assets /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=build /out/sekai-master-api /usr/local/bin/sekai-master-api
COPY --from=build /out/env/ /app/
COPY deploy/compose/app/entrypoint.sh /usr/local/bin/entrypoint.sh

RUN chmod +x /usr/local/bin/entrypoint.sh && \
	if [ -f /app/.env.development.local ]; then mv /app/.env.development.local /app/.env.development.local.baked; fi

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
