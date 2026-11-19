# Build stage
FROM golang:1.24-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ARG GIT_COMMIT=unknown
RUN CGO_ENABLED=0 go build \
    -ldflags "-X 'main.Version=${VERSION}' -X 'main.GitCommit=${GIT_COMMIT}' -X 'main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" \
    -o /out/freescout-notifier .

# Runtime stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 -h /var/lib/freescout-notifier notifier
COPY --from=build /out/freescout-notifier /usr/local/bin/freescout-notifier
USER notifier
VOLUME /var/lib/freescout-notifier
ENTRYPOINT ["freescout-notifier"]
CMD ["--config-file", "/etc/freescout-notifier/config.json", "--db-path", "/var/lib/freescout-notifier/notifications.db"]
