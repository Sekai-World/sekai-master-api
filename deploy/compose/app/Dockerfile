FROM golang:1.25-alpine AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY docs/ docs/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=$(go env GOARCH) \
	go build -buildvcs=false -o /out/sekai-master-api ./cmd/api

COPY .env.development .env.example .env.test /tmp/env/

RUN mkdir -p /out/env && \
	find /tmp/env -maxdepth 1 -type f -name '.env*' -exec cp {} /out/env/ \;

FROM alpine:3.22

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

COPY --from=build /out/sekai-master-api /usr/local/bin/sekai-master-api
COPY --from=build /out/env/ /app/
COPY deploy/compose/app/entrypoint.sh /usr/local/bin/entrypoint.sh

RUN chmod +x /usr/local/bin/entrypoint.sh && \
	if [ -f /app/.env.development.local ]; then mv /app/.env.development.local /app/.env.development.local.baked; fi

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
