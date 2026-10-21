"""FastAPI dependencies that hand routes the AWS wrappers main.py builds at
startup. Tests override these with fakes."""

from fastapi import Request

from app.aws import GetPresigner, PutPresigner
from app.jobs import JobStore


def get_presigner(request: Request) -> PutPresigner:
    return request.app.state.presigner


def get_output_presigner(request: Request) -> GetPresigner:
    return request.app.state.output_presigner


def get_job_store(request: Request) -> JobStore:
    return request.app.state.jobs
