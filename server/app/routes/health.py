"""Liveness and readiness probes."""

from fastapi import APIRouter

router = APIRouter()


@router.get("/health")
def health() -> dict[str, str]:
    """Reports that the process is up and serving requests."""
    return {"status": "ok"}


@router.get("/ready")
def ready() -> dict[str, str]:
    """Reports that the API can accept requests. It has no dependencies to
    check per request yet, so it always succeeds."""
    return {"status": "ready"}
