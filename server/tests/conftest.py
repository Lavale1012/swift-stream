import boto3
import pytest
from fastapi.testclient import TestClient

from app.dependencies import get_job_store, get_output_presigner, get_presigner
from app.jobs import STATUS_PENDING, Job
from app.main import app


class FakePresigner:
    def __init__(self, error: Exception | None = None) -> None:
        self.calls: list[tuple[str, str, int, int]] = []
        self.error = error

    def presign_put(self, key: str, content_type: str, size: int, expires_in: int) -> str:
        self.calls.append((key, content_type, size, expires_in))
        if self.error:
            raise self.error
        return "https://example.test/signed"


class FakeGetPresigner:
    def __init__(self) -> None:
        self.calls: list[tuple[str, int, str | None]] = []
        self.error: Exception | None = None

    def presign_get(self, key: str, expires_in: int, download_as: str | None = None) -> str:
        self.calls.append((key, expires_in, download_as))
        if self.error:
            raise self.error
        return "https://example.test/download" if download_as else "https://example.test/play"


class FakeJobStore:
    def __init__(self) -> None:
        self.jobs: dict[str, Job] = {}
        self.created: list[tuple[str, str]] = []
        self.error: Exception | None = None

    def create(self, job_id: str, source_key: str) -> None:
        if self.error:
            raise self.error
        self.created.append((job_id, source_key))
        self.jobs[job_id] = Job(status=STATUS_PENDING, output_key=None)

    def get(self, job_id: str) -> Job | None:
        if self.error:
            raise self.error
        return self.jobs.get(job_id)


@pytest.fixture
def presigner():
    return FakePresigner()


@pytest.fixture
def output_presigner():
    return FakeGetPresigner()


@pytest.fixture
def jobs():
    return FakeJobStore()


@pytest.fixture
def client(presigner, output_presigner, jobs):
    # TestClient is not used as a context manager, so the startup code that
    # talks to AWS does not run.
    app.dependency_overrides[get_presigner] = lambda: presigner
    app.dependency_overrides[get_output_presigner] = lambda: output_presigner
    app.dependency_overrides[get_job_store] = lambda: jobs
    yield TestClient(app)
    app.dependency_overrides.clear()


@pytest.fixture
def aws_session() -> boto3.Session:
    """Fake credentials: enough to presign (no network call) or to build a
    client for botocore's Stubber."""
    return boto3.Session(
        aws_access_key_id="AKIDEXAMPLE", aws_secret_access_key="secret", region_name="us-east-1"
    )
