#!/usr/bin/env bash
# Dev smoke test for the deployed worker: uploads a short video to the raw
# uploads bucket the way the API's presigned uploads do
# (uploads/<job id>/source.mp4), waits for the worker to finish the job, and
# reports the job record, the output and the worker's logs.
#
# It skips the API: the worker creates the job record itself instead of
# taking over a pending one made by POST /upload.
#
# Usage, from anywhere:
#   scripts/smoke-test-worker.sh              # generates a 10-second test clip
#   scripts/smoke-test-worker.sh my-clip.mp4  # uses your own video
#
# Needs aws, terraform (with infra/ initialised against the state bucket) and,
# without a video argument, ffmpeg. Bucket, table and region names come from
# the Terraform outputs.
#
# Environment overrides:
#   TIMEOUT    seconds to wait for the job (default 180)
#   LOG_GROUP  worker's log group (default /aws/ecs/worker/worker)

set -euo pipefail

TIMEOUT="${TIMEOUT:-180}"
LOG_GROUP="${LOG_GROUP:-/aws/ecs/worker/worker}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

for cmd in aws terraform; do
  command -v "$cmd" >/dev/null || { echo "error: $cmd is not installed" >&2; exit 1; }
done

tf_output() { terraform -chdir="$ROOT/infra" output -raw "$1"; }

echo "Reading Terraform outputs..."
AWS_REGION="$(tf_output aws_region)"
export AWS_REGION
RAW_BUCKET="$(tf_output raw_uploads_bucket_name)"
OUTPUT_BUCKET="$(tf_output processed_bucket_name)"
JOBS_TABLE="$(tf_output jobs_table_name)"
CLUSTER="$(tf_output ecs_cluster_name)"

# Without a running task the upload just waits in the queue, so say so up front.
running="$(aws ecs describe-services --cluster "$CLUSTER" --services worker \
  --query 'services[0].runningCount' --output text)"
if [[ "$running" == "0" || "$running" == "None" ]]; then
  echo "warning: no worker task is running (set worker_desired_count = 1 and apply)." >&2
  echo "         The job will wait in the queue until one starts." >&2
fi

# The video: the argument, or a generated clip with a 440 Hz tone (the worker
# extracts audio, so the clip needs an audio stream).
if [[ $# -ge 1 ]]; then
  VIDEO="$1"
  [[ -f "$VIDEO" ]] || { echo "error: $VIDEO not found" >&2; exit 1; }
else
  command -v ffmpeg >/dev/null || { echo "error: ffmpeg is needed to generate a clip; pass a video instead" >&2; exit 1; }
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  VIDEO="$TMP/clip.mp4"
  echo "Generating a 10-second test clip..."
  ffmpeg -loglevel error \
    -f lavfi -i testsrc=duration=10:size=640x360:rate=30 \
    -f lavfi -i sine=frequency=440:duration=10 \
    -c:v libx264 -c:a aac -shortest "$VIDEO"
fi

# 32 lowercase hex characters, the same form as the API's job IDs.
JOB="$(uuidgen | tr -d - | tr '[:upper:]' '[:lower:]')"
KEY="uploads/$JOB/source.mp4"
START="$(date +%s)"

echo "Job:    $JOB"
echo "Upload: s3://$RAW_BUCKET/$KEY"
aws s3 cp --only-show-errors "$VIDEO" "s3://$RAW_BUCKET/$KEY"

echo "Waiting up to ${TIMEOUT}s for the worker..."
status="None"
while (( $(date +%s) - START < TIMEOUT )); do
  status="$(aws dynamodb get-item --table-name "$JOBS_TABLE" --consistent-read \
    --key "{\"job_id\":{\"S\":\"$JOB\"}}" --query 'Item.status.S' --output text)"
  [[ "$status" == "completed" || "$status" == "failed" ]] && break
  printf '  status: %s (%ss)\n' "$status" "$(( $(date +%s) - START ))"
  sleep 3
done

echo
echo "Worker logs since the upload:"
aws logs tail "$LOG_GROUP" --since "$(( $(date +%s) - START + 10 ))s" || true
echo

case "$status" in
  completed)
    output_key="$(aws dynamodb get-item --table-name "$JOBS_TABLE" --consistent-read \
      --key "{\"job_id\":{\"S\":\"$JOB\"}}" --query 'Item.output_key.S' --output text)"
    echo "PASS: job completed in $(( $(date +%s) - START ))s"
    echo "Output: s3://$OUTPUT_BUCKET/$output_key"
    aws s3 ls "s3://$OUTPUT_BUCKET/$output_key"
    ;;
  failed)
    aws dynamodb get-item --table-name "$JOBS_TABLE" --consistent-read \
      --key "{\"job_id\":{\"S\":\"$JOB\"}}" \
      --query '{attempt: Item.attempt.N, error: Item.error.S}' --output table
    echo "FAIL: the job failed. SQS redelivers it, and after job_max_receive_count" >&2
    echo "      attempts it moves to the dead-letter queue." >&2
    exit 1
    ;;
  *)
    echo "FAIL: no result after ${TIMEOUT}s (status: $status)." >&2
    echo "      None means the worker never started the job: check that a task is" >&2
    echo "      running and that the queue received the upload event." >&2
    exit 1
    ;;
esac
