"""POST /upload. The logic is in app.handlers.uploads."""

from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel, Field, field_validator

from app.aws import PutPresigner
from app.dependencies import get_job_store, get_presigner
from app.handlers import uploads
from app.handlers.uploads import MAX_UPLOAD_BYTES, UPLOAD_EXTENSIONS
from app.jobs import JobStore

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
    job_id: str = Field(serialization_alias="jobId")
    url: str
    method: str
    headers: dict[str, str]
    key: str
    expires_in: int = Field(serialization_alias="expiresIn")


@router.post("/upload")
def create_upload(
    body: UploadRequest,
    presigner: Annotated[PutPresigner, Depends(get_presigner)],
    jobs: Annotated[JobStore, Depends(get_job_store)],
) -> UploadResponse:
    try:
        upload = uploads.create_upload(presigner, jobs, body.content_type, body.size)
    except uploads.UploadError:
        raise HTTPException(status_code=500, detail="could not create upload URL") from None

    return UploadResponse(
        job_id=upload.job_id,
        url=upload.url,
        method="PUT",
        headers={"Content-Type": body.content_type},
        key=upload.key,
        expires_in=upload.expires_in,
    )
