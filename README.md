# swift-stream

A cloud video processing pipeline modeled on how Netflix prepares content for streaming. You upload a video, and the pipeline turns it into:

- an **adaptive bitrate (ABR) ladder** of video renditions, packaged as HLS
- a **standalone audio track**, as an HLS audio rendition and as a downloadable file
- **subtitles** generated from speech and translated into several languages, delivered as WebVTT tracks in the HLS master playlist

A small web app lets you upload a video, watch each pipeline stage progress, and play the result with quality and subtitle-language selectors.

This is a learning and portfolio project. The point is to build the core of the pipeline myself (FFmpeg orchestration, job state, retries, scaling) instead of handing it to a managed transcoding service.

> **Status:** work in progress. See [Roadmap](#roadmap) for what runs today and what is still planned.

## Architecture

```text
Browser --presigned URL--> S3 (raw uploads)
S3 event --> SQS --> orchestrator
  probe (ffprobe)
    -> [transcode ladder || extract audio || transcribe]
    -> translate subtitles
    -> package (HLS master playlist with video, audio, and subtitle groups)
  -> S3 (processed) --> CloudFront --> player
Job/stage state --> DynamoDB --> status page
```

1. The browser asks the API for a presigned S3 URL and uploads the video straight to S3. Video bytes never pass through the API.
2. S3 sends an event notification to an SQS queue.
3. Go workers on ECS Fargate pull jobs from the queue, run FFmpeg, write the output to a processed bucket, and record each job's state in DynamoDB.
4. CloudFront serves the HLS output to an hls.js player.

The workers scale on queue depth and scale to zero when there is nothing to do.

## Tech stack

| Area | Choice |
| --- | --- |
| Workers | Go (standard library, `log/slog`, `context`) |
| API | Python 3.11+, FastAPI, boto3 |
| Media | FFmpeg and ffprobe |
| Infrastructure | Terraform |
| Compute | ECS on Fargate, scaled on SQS queue depth |
| Storage and state | S3, DynamoDB |
| Messaging | SQS with a dead-letter queue |
| Speech and translation | Whisper or Amazon Transcribe (undecided); Amazon Translate |
| Delivery | CloudFront |
| Frontend | React 19, TypeScript, Vite, hls.js |

## Repository layout

```text
server/   HTTP API (FastAPI): issues presigned upload URLs
worker/   Go worker: consumes SQS jobs, runs FFmpeg, records job state
client/   Web app (React + Vite), currently an empty scaffold
infra/    Terraform for the AWS resources
```

### `server/`: API

- `POST /upload` takes `{ "contentType": ..., "size": ... }` and returns a presigned S3 PUT URL that expires after 15 minutes. The object key is `uploads/<job id>/source.<ext>`. The content type and exact size are part of the signature, so S3 rejects an upload that doesn't match what was requested.
- `GET /health` and `GET /ready` are for liveness and readiness checks.
- Routes (`app/routes/`) hold only the HTTP layer. The logic lives in `app/handlers/`, which doesn't import FastAPI, so it can be tested without HTTP.
- At startup the API checks its AWS credentials with an STS call and exits if they don't work.

### `worker/`: Go worker

A message flows through four packages: `consumer` → `job` → `process` → `media`.

- **`consumer`** long-polls SQS one message at a time. A heartbeat goroutine keeps extending the message's visibility timeout while the job runs, so a crashed worker holds a message for at most two minutes. After five failed deliveries the worker gives up on the message. On SIGTERM it releases the message for another worker instead of finishing the job.
- **`job`** parses the S3 event, downloads the source to a scratch directory, runs the processing step, uploads the result, and records progress in DynamoDB.
- **`media`** runs FFmpeg and ffprobe with timeouts, keeps the tail of stderr for error messages, and checks the output with ffprobe. Today it extracts the audio track as MP3. Transcoding and packaging come next.
- **`jobstate`** stores one DynamoDB item per job. Its updates are conditional, so a late duplicate message can't reopen or fail a job that has already completed.
- **`workdir`** gives each worker process its own scratch directory and holds a file lock on it. At startup, a worker deletes directories left behind by workers that crashed without cleaning up.

### `infra/`: Terraform

Provisioned so far: a VPC, an ECS cluster (Fargate and Fargate Spot), and three S3 buckets: raw uploads (with a CORS rule for browser uploads), processed output, and the web frontend. SQS, DynamoDB, ECR, and the ECS services come next.

## Design notes

Building it this way raised a few problems worth calling out:

- **At-least-once delivery.** SQS can deliver a message more than once, so every step has to be idempotent. The output key comes from the source key, so a redelivered job overwrites the same object. Job state updates are conditional on the job not already being complete.
- **Keyframe alignment.** Every rendition in the ladder needs keyframes at the same timestamps, and the segment length must be a multiple of the GOP length. Otherwise the player can't switch renditions cleanly. This matters most once encoding is split into chunks that run in parallel.
- **Timeouts and cancellation.** Every FFmpeg run is bound to a `context`, so a hung encode is killed and a shutdown stops work promptly. The worker uses `ffmpeg-go` only to build the argument list and starts the process itself, because the library's own `Run` silently drops the context (a test guards this).
- **No static credentials.** Locally, both services use the AWS default credential chain. On ECS they will use least-privilege task roles.
- **Cost.** Testing uses 30–60 second clips (for example, Blender's open movies), and a budget alarm is in place.

## Running locally

### Prerequisites

- Go 1.26+
- Python 3.11+
- [Bun](https://bun.sh)
- FFmpeg and ffprobe on your `PATH`
- AWS credentials and a region, set through environment variables or `~/.aws`

### API

```sh
cd server
python3 -m venv .venv && .venv/bin/pip install -e ".[dev]"
UPLOAD_BUCKET=<bucket> .venv/bin/python -m app    # serves on :8080; set PORT to change it
.venv/bin/pytest
```

Interactive API docs are at `http://localhost:8080/docs`. If the web app runs on a different origin, add it to `CORS_ALLOWED_ORIGINS` (comma-separated; the default is `http://localhost:5173`).

### Worker

```sh
cd worker
JOB_QUEUE_URL=<queue url> OUTPUT_BUCKET=<bucket> JOBS_TABLE=<table> go run .
go test ./...
```

`WORK_DIR` sets the scratch directory (optional; the default is a directory under `$TMPDIR`). The `media` tests run real FFmpeg on generated clips and skip if FFmpeg isn't installed. The DynamoDB integration test runs only when `DYNAMODB_ENDPOINT` is set, for example against DynamoDB Local.

So far the full worker flow (S3, SQS, DynamoDB, and real FFmpeg) has been run against a local [moto](https://github.com/getmoto/moto) server, not against real AWS.

### Client

```sh
cd client
bun install
bun run dev
```

### Infrastructure

```sh
cd infra
terraform init
terraform plan -var 'availability_zones=["us-east-1a","us-east-1b"]' \
  -var raw_uploads_bucket_name=<name> \
  -var processed_bucket_name=<name> \
  -var web_bucket_name=<name>
```

The NAT gateway is on by default and bills by the hour. Set `enable_nat_gateway=false` to avoid that while experimenting.

## Roadmap

- [x] **0. FFmpeg locally:** a 3-rendition HLS ladder with a separate audio group and a master playlist, played in hls.js
- [ ] **1. Go worker locally:** wraps FFmpeg, handles errors and timeouts, runs in Docker *(in progress: SQS consumer, job state, and audio extraction work; progress reporting and Docker remain)*
- [ ] **2. Terraform cloud skeleton:** S3, SQS + DLQ, DynamoDB, ECR, the presigned-upload API, and S3 → SQS wiring *(started: VPC, ECS cluster, S3 buckets)*
- [ ] **3. Workers on ECS Fargate:** scale on queue depth, down to zero when idle, with task role IAM
- [ ] **4. Orchestration:** split each job into stages with parallel branches and track each stage's state
- [ ] **5. Subtitles:** speech-to-text → WebVTT → translations → subtitle tracks in the master playlist
- [ ] **6. Chunked parallel encoding:** split at keyframes, encode chunks across workers, stitch them back together, and benchmark against a single worker
- [ ] **7. Per-title encoding:** use complexity analysis and VMAF scores to choose a custom bitrate ladder for each video
- [ ] **8. Frontend, CloudFront, observability:** player app, status page, and per-stage metrics
