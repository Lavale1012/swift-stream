import re
from urllib.parse import parse_qs, urlsplit

import pytest
from botocore.exceptions import ClientError, NoCredentialsError
from fastapi.testclient import TestClient

from app.aws import S3PutPresigner, s3_client
from app.main import allowed_origins, app


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
def test_upload_rejects_bad_requests(client, presigner, jobs, body):
    response = client.post("/upload", content=body, headers={"Content-Type": "application/json"})
    assert response.status_code == 422, response.text
    assert presigner.calls == []
    assert jobs.created == []


def test_upload_rejects_form_file(client, presigner, jobs):
    response = client.post("/upload", files={"video": b"video bytes"})
    assert response.status_code == 422, response.text
    assert presigner.calls == []
    assert jobs.created == []


def test_upload_returns_presigned_url(client, presigner, jobs):
    response = client.post("/upload", json={"contentType": "video/quicktime", "size": 1 << 30})
    assert response.status_code == 200, response.text

    body = response.json()
    job_id = body["jobId"]
    assert re.fullmatch(r"[0-9a-f]{32}", job_id)
    assert body == {
        "jobId": job_id,
        "url": "https://example.test/signed",
        "method": "PUT",
        "headers": {"Content-Type": "video/quicktime"},
        "key": f"uploads/{job_id}/source.mov",
        "expiresIn": 900,
    }
    assert presigner.calls == [(body["key"], "video/quicktime", 1 << 30, 900)]
    assert jobs.created == [(job_id, body["key"])]


def test_upload_job_ids_are_unique(client):
    body = {"contentType": "video/mp4", "size": 100}
    first = client.post("/upload", json=body).json()["jobId"]
    second = client.post("/upload", json=body).json()["jobId"]
    assert first != second


def test_upload_hides_presign_error(client, presigner, jobs):
    presigner.error = NoCredentialsError()
    response = client.post("/upload", json={"contentType": "video/mp4", "size": 100})
    assert response.status_code == 500
    assert response.json() == {"detail": "could not create upload URL"}
    assert jobs.created == [], "a failed presign must not leave a job behind"


def test_upload_hides_job_store_error(client, jobs):
    jobs.error = ClientError({"Error": {"Code": "ResourceNotFoundException"}}, "PutItem")
    response = client.post("/upload", json={"contentType": "video/mp4", "size": 100})
    assert response.status_code == 500
    assert response.json() == {"detail": "could not create upload URL"}


def test_presigned_url_signs_size_and_type(aws_session):
    """Runs the real boto3 signer to pin down what the signature covers."""
    url = S3PutPresigner(s3_client(aws_session), "raw-bucket").presign_put(
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


@pytest.mark.parametrize("missing", ["UPLOAD_BUCKET", "OUTPUT_BUCKET", "JOBS_TABLE"])
def test_startup_fails_without_config(monkeypatch, missing):
    for name in ("UPLOAD_BUCKET", "OUTPUT_BUCKET", "JOBS_TABLE"):
        monkeypatch.setenv(name, "configured")
    monkeypatch.delenv(missing)
    with pytest.raises(RuntimeError, match=f"{missing} is not set"):
        with TestClient(app):
            pass


def test_cors_preflight_allows_the_web_app(client):
    response = client.options(
        "/upload",
        headers={
            "Origin": "http://localhost:5173",
            "Access-Control-Request-Method": "POST",
            "Access-Control-Request-Headers": "content-type",
        },
    )
    assert response.status_code == 200, response.text
    assert response.headers["access-control-allow-origin"] == "http://localhost:5173"
    assert "POST" in response.headers["access-control-allow-methods"]
    assert "content-type" in response.headers["access-control-allow-headers"].lower()


def test_cors_preflight_rejects_other_origins(client):
    response = client.options(
        "/upload",
        headers={"Origin": "https://evil.example", "Access-Control-Request-Method": "POST"},
    )
    assert response.status_code == 400
    assert "access-control-allow-origin" not in response.headers


def test_cors_headers_on_the_upload_response(client):
    response = client.post(
        "/upload",
        json={"contentType": "video/mp4", "size": 100},
        headers={"Origin": "http://localhost:5173"},
    )
    assert response.status_code == 200, response.text
    assert response.headers["access-control-allow-origin"] == "http://localhost:5173"


@pytest.mark.parametrize(
    ("value", "want"),
    [
        pytest.param(None, ["http://localhost:5173"], id="unset uses the dev server"),
        pytest.param("", ["http://localhost:5173"], id="empty uses the dev server"),
        pytest.param("https://app.example.com/", ["https://app.example.com"], id="trailing slash dropped"),
        pytest.param(
            " https://app.example.com , http://localhost:5173 ,",
            ["https://app.example.com", "http://localhost:5173"],
            id="list with spaces",
        ),
    ],
)
def test_allowed_origins(value, want):
    assert allowed_origins(value) == want
