# syntax=docker/dockerfile:1

# Build from worker/:
#   docker build -t swift-stream-worker .
# For Fargate on Graviton (ARM), or x86:
#   docker build --platform linux/arm64 -t swift-stream-worker .
#   docker build --platform linux/amd64 -t swift-stream-worker .

# Build stage. It runs on the build machine's own architecture and
# cross-compiles for the target, which Go does without emulation, so an ARM
# image builds as fast as a native one.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src

# Dependencies first, so editing code does not re-download them.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# CGO off gives a static binary that needs no C libraries in the runtime image.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/worker .

# Runtime stage: the binary plus FFmpeg. Alpine keeps the image small, which
# shortens the image pull when a task starts from zero.
FROM alpine:3.22

# Alpine's ffmpeg package includes ffprobe and the libmp3lame encoder that
# media.ExtractAudio uses. ca-certificates is for HTTPS to AWS.
RUN apk add --no-cache ffmpeg ca-certificates \
    && adduser -D -H -u 10001 worker

COPY --from=build /out/worker /usr/local/bin/worker

# Job files go under WORK_DIR (see worker/workdir). On Fargate, /tmp is part of
# the task's ephemeral storage, 20 GiB by default.
ENV WORK_DIR=/tmp/swift-stream-worker

USER worker

# Exec form, so the worker is PID 1 and receives the SIGTERM that ECS sends on
# scale-in and Spot interruption, and releases its current message.
ENTRYPOINT ["/usr/local/bin/worker"]
