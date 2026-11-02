# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X github.com/guanlanai/guanlanai/internal/version.Version=${VERSION} -X github.com/guanlanai/guanlanai/internal/version.Commit=${COMMIT}" \
    -o /out/guanlanai ./cmd/bot

FROM alpine:3.21
RUN addgroup -g 1000 -S app && adduser -u 1000 -S -G app -h /app app \
    && apk add --no-cache ca-certificates \
    && mkdir -p /data && chown app:app /data

WORKDIR /app
COPY --from=build /out/guanlanai /usr/local/bin/guanlanai

USER app
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/guanlanai"]
