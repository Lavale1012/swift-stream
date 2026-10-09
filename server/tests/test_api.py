import re
from urllib.parse import parse_qs, urlsplit

import boto3
import pytest
from botocore.exceptions import NoCredentialsError
from fastapi.testclient import TestClient

from app.aws import S3PutPresigner, s3_client
from app.main import app
from app.uploads import get_presigner


class FakePresigner:
    def __init__(self, error: Exception | None = None) -> None:
        self.calls: list[tuple[str, str, int, int]] = []
        self.error = error

    def presign_put(self, key: str, content_type: str, size: int, expires_in: int) -> str:
        self.calls.append((key, content_type, size, expires_in))
        if self.error:
            raise self.error
        return "https://example.test/signed"


@pytest.fixture
def presigner():
    return FakePresigner()


@pytest.fixture
def client(presigner):
    # TestClient is not used as a context manager, so the startup code that
    # talks to AWS does not run.
    app.dependency_overrides[get_presigner] = lambda: presigner
    yield TestClient(app)
    app.dependency_overrides.clear()


def test_health_and_ready(client):
    assert client.get("/health").json() == {"status": "ok"}
    assert client.get("/ready").json() == {"status": "ready"}


@pytest.mark.parametrize(
    "body",
    [
        pytest.param("video bytes", id="not json"),
        pytest.param("", id="empty body"),
        pytest.param('{"size": 100}', id="missing content type"),
        pytest.param('{"contentType": "application/pdf", "size": 100}', id="not a video"),
        pytest.param('{"contentType": "video/mp4"}', id="missing size"),
        pytest.param('{"contentType": "video/mp4", "size": 0}', id="zero size"),
        pytest.param('{"contentType": "video/mp4", "size": -1}', id="negative size"),
        pytest.param('{"contentType": "video/mp4", "size": 1073741825}', id="too large"),
        pytest.param('{"contentType": "video/mp4", "size": "100"}', id="size as string"),
    ],
)
def test_upload_rejects_bad_requests(client, presigner, body):
    response = client.post("/upload", content=body, headers={"Content-Type": "application/json"})
    assert response.status_code == 422, response.text
    assert presigner.calls == []


def test_upload_rejects_form_file(client, presigner):
    response = client.post("/upload", files={"video": b"video bytes"})
    assert response.status_code == 422, response.text
    assert presigner.calls == []


def test_upload_returns_presigned_url(client, presigner):
    response = client.post("/upload", json={"contentType": "video/quicktime", "size": 1 << 30})
    assert response.status_code == 200, response.text

    body = response.json()
    assert re.fullmatch(r"uploads/[0-9a-f]{32}/source\.mov", body["key"])
    assert body == {
        "url": "https://example.test/signed",
        "method": "PUT",
        "headers": {"Content-Type": "video/quicktime"},
        "key": body["key"],
        "expiresIn": 900,
    }
    assert presigner.calls == [(body["key"], "video/quicktime", 1 << 30, 900)]


def test_upload_keys_are_unique(client):
    body = {"contentType": "video/mp4", "size": 100}
    first = client.post("/upload", json=body).json()["key"]
    second = client.post("/upload", json=body).json()["key"]
    assert first != second


def test_upload_hides_presign_error(client, presigner):
    presigner.error = NoCredentialsError()
    response = client.post("/upload", json={"contentType": "video/mp4", "size": 100})
    assert response.status_code == 500
    assert response.json() == {"detail": "could not create upload URL"}


def test_presigned_url_signs_size_and_type():
    """Runs the real boto3 signer with fake credentials (presigning makes no
    network call) to pin down what the signature covers."""
    session = boto3.Session(
        aws_access_key_id="AKIDEXAMPLE", aws_secret_access_key="secret", region_name="us-east-1"
    )
    url = S3PutPresigner(s3_client(session), "raw-bucket").presign_put(
        "uploads/abc/source.mp4", "video/mp4", 4096, 900
    )

    parts = urlsplit(url)
    assert parts.hostname.startswith("raw-bucket.s3.")
    assert parts.path == "/uploads/abc/source.mp4"

    query = parse_qs(parts.query)
    assert query["X-Amz-Algorithm"] == ["AWS4-HMAC-SHA256"]
    assert query["X-Amz-Expires"] == ["900"]
    # A browser cannot add headers or query values it was not told about, so
    # anything else in the signature would make the upload fail.
    assert query["X-Amz-SignedHeaders"] == ["content-length;content-type;host"]
    assert not [name for name in query if "checksum" in name.lower()]


def test_startup_fails_without_upload_bucket(monkeypatch):
    monkeypatch.delenv("UPLOAD_BUCKET", raising=False)
    with pytest.raises(RuntimeError, match="UPLOAD_BUCKET is not set"):
        with TestClient(app):
            pass
