"""Presigned upload URLs: the browser sends the video straight to S3, so the
bytes never pass through this service."""

import logging
import uuid
from dataclasses import dataclass

from botocore.exceptions import BotoCoreError, ClientError

from app.aws import PutPresigner

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


class PresignError(Exception):
    """S3 could not sign the upload URL. The cause is logged, not carried,
    so callers cannot leak it to the client."""


@dataclass(frozen=True)
class PresignedUpload:
    url: str
    key: str
    expires_in: int


def create_upload(presigner: PutPresigner, content_type: str, size: int) -> PresignedUpload:
    """Picks a new object key for a video of content_type and exactly size
    bytes, and returns a URL that lets the client PUT it there. The caller
    must have checked content_type against UPLOAD_EXTENSIONS and size against
    MAX_UPLOAD_BYTES."""
    key = f"uploads/{uuid.uuid4().hex}/source{UPLOAD_EXTENSIONS[content_type]}"
    try:
        url = presigner.presign_put(key, content_type, size, UPLOAD_URL_EXPIRY_SECONDS)
    except (BotoCoreError, ClientError):
        logger.exception("presign upload failed key=%s", key)
        raise PresignError from None

    return PresignedUpload(url=url, key=key, expires_in=UPLOAD_URL_EXPIRY_SECONDS)
