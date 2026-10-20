"""AWS setup and the S3 presigners: PUT into the upload bucket for the upload
route, GET from the output bucket for the media routes."""

import logging
from typing import Protocol

import boto3
from botocore.config import Config

logger = logging.getLogger(__name__)

TIMEOUTS = {"connect_timeout": 5, "read_timeout": 5}


class PutPresigner(Protocol):
    """What the upload route needs from S3, so tests can pass a fake."""

    def presign_put(self, key: str, content_type: str, size: int, expires_in: int) -> str: ...


class S3PutPresigner:
    def __init__(self, client, bucket: str) -> None:
        self._client = client
        self._bucket = bucket

    def presign_put(self, key: str, content_type: str, size: int, expires_in: int) -> str:
        # ContentType and ContentLength become signed headers: S3 rejects an
        # upload whose type or exact size differs from what was signed.
        return self._client.generate_presigned_url(
            "put_object",
            Params={
                "Bucket": self._bucket,
                "Key": key,
                "ContentType": content_type,
                "ContentLength": size,
            },
            ExpiresIn=expires_in,
        )


class GetPresigner(Protocol):
    """What the media routes need from the output bucket, so tests can pass a
    fake."""

    def presign_get(self, key: str, expires_in: int, download_as: str | None = None) -> str: ...


class S3GetPresigner:
    def __init__(self, client, bucket: str) -> None:
        self._client = client
        self._bucket = bucket

    def presign_get(self, key: str, expires_in: int, download_as: str | None = None) -> str:
        # S3 sends ResponseContentDisposition back as the Content-Disposition
        # header. "inline" lets the browser play the file (an <audio> element
        # streams it with Range requests, so seeking works); "attachment"
        # makes it save the file as download_as instead. It is a signed query
        # parameter, not a header the client has to send.
        disposition = f'attachment; filename="{download_as}"' if download_as else "inline"
        return self._client.generate_presigned_url(
            "get_object",
            Params={
                "Bucket": self._bucket,
                "Key": key,
                "ResponseContentDisposition": disposition,
            },
            ExpiresIn=expires_in,
        )


def s3_client(session: boto3.Session):
    # SigV4 is required for the signed headers above; older defaults can
    # fall back to SigV2 for presigned URLs.
    return session.client("s3", config=Config(signature_version="s3v4", **TIMEOUTS))


def connect() -> boto3.Session:
    """Load credentials and region from the default chain (environment
    variables, the ~/.aws files, or an attached IAM role), confirm they work
    by asking STS who we are, and return the session."""
    session = boto3.Session()
    if session.region_name is None:
        raise RuntimeError("aws region not set: set AWS_REGION or a region in ~/.aws/config")

    identity = session.client("sts", config=Config(**TIMEOUTS)).get_caller_identity()
    logger.info("aws: authenticated as %s in %s", identity["Arn"], session.region_name)
    return session
