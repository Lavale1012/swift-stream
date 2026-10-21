"""GET /jobs/{job_id}/audio. The logic is in app.handlers.media."""

from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Path
from pydantic import BaseModel, Field

from app.aws import GetPresigner
from app.dependencies import get_job_store, get_output_presigner
from app.handlers import media
from app.handlers.media import JOB_ID_PATTERN
from app.jobs import JobStore

router = APIRouter()


class AudioResponse(BaseModel):
    play_url: str = Field(serialization_alias="playUrl")
    download_url: str = Field(serialization_alias="downloadUrl")
    expires_in: int = Field(serialization_alias="expiresIn")


@router.get("/jobs/{job_id}/audio")
def get_audio(
    job_id: Annotated[str, Path(pattern=JOB_ID_PATTERN)],
    jobs: Annotated[JobStore, Depends(get_job_store)],
    presigner: Annotated[GetPresigner, Depends(get_output_presigner)],
) -> AudioResponse:
    try:
        urls = media.audio_urls(jobs, presigner, job_id)
    except media.JobNotFound:
        raise HTTPException(status_code=404, detail="job not found") from None
    except media.MediaNotReady as err:
        # 409: the job exists, but its current state has no audio. The
        # client can poll until the status changes.
        raise HTTPException(status_code=409, detail=f"audio not ready: job is {err.status}") from None
    except media.MediaError:
        raise HTTPException(status_code=500, detail="could not create audio URLs") from None

    return AudioResponse(
        play_url=urls.play_url, download_url=urls.download_url, expires_in=urls.expires_in
    )
