"""URLs for a job's processed media, which the worker writes to the output
bucket and records on the job (see app.jobs)."""

import logging
import posixpath
from dataclasses import dataclass

from botocore.exceptions import BotoCoreError, ClientError

from app.aws import GetPresigner
from app.jobs import STATUS_COMPLETED, JobStore

logger = logging.getLogger(__name__)

# A job ID is the uuid4().hex that app.handlers.uploads puts in the upload key.
JOB_ID_PATTERN = r"^[0-9a-f]{32}$"
MEDIA_URL_EXPIRY_SECONDS = 15 * 60


class JobNotFound(Exception):
    """No job has this ID."""


class MediaNotReady(Exception):
    """The job exists but has no output yet: it is pending, processing, or
    failed (SQS may still retry it)."""

    def __init__(self, status: str) -> None:
        super().__init__(status)
        self.status = status


class MediaError(Exception):
    """DynamoDB or S3 failed. The cause is logged, not carried, so callers
    cannot leak it to the client."""


@dataclass(frozen=True)
class AudioUrls:
    play_url: str
    download_url: str
    expires_in: int


def audio_urls(jobs: JobStore, presigner: GetPresigner, job_id: str) -> AudioUrls:
    """Returns two URLs for the audio extracted from job_id's video: one a
    browser can play in place, one that downloads it as a file. The caller
    must have checked job_id against JOB_ID_PATTERN."""
    try:
        job = jobs.get(job_id)
    except (BotoCoreError, ClientError):
        logger.exception("read job failed job_id=%s", job_id)
        raise MediaError from None

    if job is None:
        raise JobNotFound
    if job.status != STATUS_COMPLETED:
        raise MediaNotReady(job.status)
    # The worker writes one output per job today, the audio. Once it writes
    # more, the job record needs a key per output.
    if not job.output_key:
        logger.error("completed job has no output_key job_id=%s", job_id)
        raise MediaError

    expires_in = MEDIA_URL_EXPIRY_SECONDS
    try:
        play_url = presigner.presign_get(job.output_key, expires_in)
        download_url = presigner.presign_get(
            job.output_key, expires_in, download_as=posixpath.basename(job.output_key)
        )
    except (BotoCoreError, ClientError):
        logger.exception("presign audio failed job_id=%s key=%s", job_id, job.output_key)
        raise MediaError from None

    return AudioUrls(play_url=play_url, download_url=download_url, expires_in=expires_in)
