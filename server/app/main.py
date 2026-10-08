import logging
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI

from app import aws, uploads

logging.basicConfig(level=logging.INFO)


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Fail at startup, not on the first request, if configuration is missing.
    bucket = os.environ.get("UPLOAD_BUCKET")
    if not bucket:
        raise RuntimeError("UPLOAD_BUCKET is not set")
    app.state.presigner = aws.connect(bucket)
    yield


app = FastAPI(title="swift-stream API", lifespan=lifespan)
app.include_router(uploads.router)


@app.get("/health")
def health() -> dict[str, str]:
    """Reports that the process is up and serving requests."""
    return {"status": "ok"}


@app.get("/ready")
def ready() -> dict[str, str]:
    """Reports that the API can accept requests. It has no dependencies to
    check per request yet, so it always succeeds."""
    return {"status": "ready"}
