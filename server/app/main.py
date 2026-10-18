import logging
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI

from app import aws
from app.routes import health, uploads

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
app.include_router(health.router)
app.include_router(uploads.router)
