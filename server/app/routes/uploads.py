"""POST /upload. The logic is in app.handlers.uploads."""

from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Request
from pydantic import BaseModel, Field, field_validator

from app.aws import PutPresigner
from app.handlers import uploads
from app.handlers.uploads import MAX_UPLOAD_BYTES, UPLOAD_EXTENSIONS

router = APIRouter()


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
    try:
        upload = uploads.create_upload(presigner, body.content_type, body.size)
    except uploads.PresignError:
        raise HTTPException(status_code=500, detail="could not create upload URL") from None

    return UploadResponse(
        url=upload.url,
        method="PUT",
        headers={"Content-Type": body.content_type},
        key=upload.key,
        expires_in=upload.expires_in,
    )
