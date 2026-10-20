"""Presigned upload URLs: the browser sends the video straight to S3, so the
bytes never pass through this service."""

import logging
import uuid
from dataclasses import dataclass

from botocore.exceptions import BotoCoreError, ClientError

from app.aws import PutPresigner
from app.jobs import JobStore

logger = logging.getLogger(__name__)

MAX_UPLOAD_BYTES = 1 << 30  # 1 GiB
UPLOAD_URL_EXPIRY_SECONDS = 15 * 60

# Maps each accepted content type to the extension used in the object key.
# The key never includes anything the client typed.
UPLOAD_EXTENSIONS = {
    "video/mp4": ".mp4",
    "video/quicktime": ".mov",
    "video/webm": ".webm",
    "video/x-matroska": ".mkv",
}


class UploadError(Exception):
    """S3 could not sign the upload URL, or the job record could not be
    written. The cause is logged, not carried, so callers cannot leak it to
    the client."""


@dataclass(frozen=True)
class PresignedUpload:
    url: str
    key: str
    job_id: str
    expires_in: int


def create_upload(
    presigner: PutPresigner, jobs: JobStore, content_type: str, size: int
) -> PresignedUpload:
    """Starts a job for a video of content_type and exactly size bytes:
    picks its object key, records the job as pending, and returns a URL that
    lets the client PUT the video there. The caller must have checked
    content_type against UPLOAD_EXTENSIONS and size against MAX_UPLOAD_BYTES."""
    job_id = uuid.uuid4().hex
    key = f"uploads/{job_id}/source{UPLOAD_EXTENSIONS[content_type]}"
    try:
        url = presigner.presign_put(key, content_type, size, UPLOAD_URL_EXPIRY_SECONDS)
        # The record must exist before the client has the URL, so the job is
        # never unknown between the upload and the worker picking it up.
        # Presigning first means a failed presign leaves no record behind.
        jobs.create(job_id, key)
    except (BotoCoreError, ClientError):
        logger.exception("create upload failed job_id=%s", job_id)
        raise UploadError from None

    return PresignedUpload(url=url, key=key, job_id=job_id, expires_in=UPLOAD_URL_EXPIRY_SECONDS)
