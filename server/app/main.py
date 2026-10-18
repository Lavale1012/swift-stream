import logging
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from app import aws
from app.routes import health, uploads

logging.basicConfig(level=logging.INFO)

# The Vite dev server, matching the default upload_cors_allowed_origins in
# infra/variable.tf.
DEFAULT_CORS_ORIGINS = "http://localhost:5173"


def allowed_origins(value: str | None) -> list[str]:
    """Parses CORS_ALLOWED_ORIGINS: a comma-separated list of origins
    (scheme, host and port), such as "https://app.example.com". A trailing
    slash is dropped, because the browser's Origin header never has one and
    the origin would otherwise silently never match."""
    origins = [o.strip().rstrip("/")
               for o in (value or DEFAULT_CORS_ORIGINS).split(",")]
    return [o for o in origins if o]


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Fail at startup, not on the first request, if configuration is missing.
    bucket = os.environ.get("UPLOAD_BUCKET")
    if not bucket:
        raise RuntimeError("UPLOAD_BUCKET is not set")
    app.state.presigner = aws.connect(bucket)
    yield


app = FastAPI(title="swift-stream API", lifespan=lifespan)

# The browser calls POST /upload from the web app's origin before it sends the
# video to S3, so the API must allow the same origins as the upload bucket's
# CORS rule. Keep CORS_ALLOWED_ORIGINS and upload_cors_allowed_origins in step.
app.add_middleware(
    CORSMiddleware,
    allow_origins=allowed_origins(os.environ.get("CORS_ALLOWED_ORIGINS")),
    allow_methods=["GET", "POST"],
    allow_headers=["Content-Type"],
    max_age=600,
)
app.include_router(health.router)
app.include_router(uploads.router)
