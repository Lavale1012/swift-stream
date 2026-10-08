"""Presigned upload URLs: the browser sends the video straight to S3, so the
bytes never pass through this service."""

import logging
import uuid
from typing import Annotated

from botocore.exceptions import BotoCoreError, ClientError
from fastapi import APIRouter, Depends, HTTPException, Request
from pydantic import BaseModel, Field, field_validator

from app.aws import PutPresigner

logger = logging.getLogger(__name__)
router = APIRouter()

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


class UploadRequest(BaseModel):
    content_type: str = Field(alias="contentType")
    size: int = Field(strict=True, gt=0, le=MAX_UPLOAD_BYTES)

    @field_validator("content_type")
    @classmethod
    def supported(cls, value: str) -> str:
        if value not in UPLOAD_EXTENSIONS:
            raise ValueError(f"must be one of {', '.join(UPLOAD_EXTENSIONS)}")
        return value


class UploadResponse(BaseModel):
    url: str
    method: str
    headers: dict[str, str]
    key: str
    expires_in: int = Field(serialization_alias="expiresIn")


def get_presigner(request: Request) -> PutPresigner:
    return request.app.state.presigner


@router.post("/upload")
def create_upload(
    body: UploadRequest,
    presigner: Annotated[PutPresigner, Depends(get_presigner)],
) -> UploadResponse:
    key = f"uploads/{uuid.uuid4().hex}/source{UPLOAD_EXTENSIONS[body.content_type]}"
    try:
        url = presigner.presign_put(key, body.content_type, body.size, UPLOAD_URL_EXPIRY_SECONDS)
    except (BotoCoreError, ClientError):
        logger.exception("presign upload failed key=%s", key)
        raise HTTPException(status_code=500, detail="could not create upload URL") from None

    return UploadResponse(
        url=url,
        method="PUT",
        headers={"Content-Type": body.content_type},
        key=key,
        expires_in=UPLOAD_URL_EXPIRY_SECONDS,
    )
