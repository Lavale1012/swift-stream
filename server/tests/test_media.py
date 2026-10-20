from urllib.parse import parse_qs, urlsplit

import pytest
from botocore.exceptions import ClientError, NoCredentialsError

from app.aws import S3GetPresigner, s3_client
from app.jobs import Job

JOB_ID = "0123456789abcdef0123456789abcdef"
AUDIO_KEY = f"uploads/{JOB_ID}/audio.mp3"


def test_audio_returns_play_and_download_urls(client, jobs, output_presigner):
    jobs.jobs[JOB_ID] = Job(status="completed", output_key=AUDIO_KEY)
    response = client.get(f"/jobs/{JOB_ID}/audio")
    assert response.status_code == 200, response.text
    assert response.json() == {
        "playUrl": "https://example.test/play",
        "downloadUrl": "https://example.test/download",
        "expiresIn": 900,
    }
    assert output_presigner.calls == [(AUDIO_KEY, 900, None), (AUDIO_KEY, 900, "audio.mp3")]


def test_audio_unknown_job(client, output_presigner):
    response = client.get(f"/jobs/{JOB_ID}/audio")
    assert response.status_code == 404
    assert response.json() == {"detail": "job not found"}
    assert output_presigner.calls == []


def test_audio_after_upload_is_pending_not_unknown(client):
    """The job exists from the moment the client has its upload URL, before
    the worker has seen it."""
    job_id = client.post("/upload", json={"contentType": "video/mp4", "size": 100}).json()["jobId"]
    response = client.get(f"/jobs/{job_id}/audio")
    assert response.status_code == 409
    assert response.json() == {"detail": "audio not ready: job is pending"}


@pytest.mark.parametrize("status", ["pending", "processing", "failed"])
def test_audio_not_ready(client, jobs, output_presigner, status):
    jobs.jobs[JOB_ID] = Job(status=status, output_key=None)
    response = client.get(f"/jobs/{JOB_ID}/audio")
    assert response.status_code == 409
    assert response.json() == {"detail": f"audio not ready: job is {status}"}
    assert output_presigner.calls == []


def test_audio_completed_without_output_key(client, jobs):
    jobs.jobs[JOB_ID] = Job(status="completed", output_key=None)
    response = client.get(f"/jobs/{JOB_ID}/audio")
    assert response.status_code == 500
    assert response.json() == {"detail": "could not create audio URLs"}


@pytest.mark.parametrize(
    "job_id",
    [
        pytest.param("not-a-job-id", id="not hex"),
        pytest.param(JOB_ID.upper(), id="uppercase"),
        pytest.param(JOB_ID[:-1], id="too short"),
        pytest.param(JOB_ID + "0", id="too long"),
        pytest.param("%2E%2E", id="encoded dot dot"),
    ],
)
def test_audio_rejects_bad_job_id(client, jobs, job_id):
    jobs.error = AssertionError("job store must not be called")
    response = client.get(f"/jobs/{job_id}/audio")
    assert response.status_code == 422, response.text


@pytest.mark.parametrize(
    "error",
    [
        pytest.param(ClientError({"Error": {"Code": "AccessDeniedException"}}, "GetItem"), id="client"),
        pytest.param(NoCredentialsError(), id="botocore"),
    ],
)
def test_audio_hides_job_store_error(client, jobs, error):
    jobs.error = error
    response = client.get(f"/jobs/{JOB_ID}/audio")
    assert response.status_code == 500
    assert response.json() == {"detail": "could not create audio URLs"}


def test_audio_hides_presign_error(client, jobs, output_presigner):
    jobs.jobs[JOB_ID] = Job(status="completed", output_key=AUDIO_KEY)
    output_presigner.error = NoCredentialsError()
    response = client.get(f"/jobs/{JOB_ID}/audio")
    assert response.status_code == 500
    assert response.json() == {"detail": "could not create audio URLs"}


@pytest.mark.parametrize(
    ("download_as", "disposition"),
    [
        pytest.param(None, "inline", id="play"),
        pytest.param("audio.mp3", 'attachment; filename="audio.mp3"', id="download"),
    ],
)
def test_get_url_sets_disposition(aws_session, download_as, disposition):
    """Runs the real boto3 signer to pin down what the URL asks S3 for."""
    url = S3GetPresigner(s3_client(aws_session), "processed-bucket").presign_get(
        AUDIO_KEY, 900, download_as=download_as
    )

    parts = urlsplit(url)
    assert parts.hostname.startswith("processed-bucket.s3.")
    assert parts.path == f"/{AUDIO_KEY}"

    query = parse_qs(parts.query)
    assert query["X-Amz-Expires"] == ["900"]
    assert query["response-content-disposition"] == [disposition]
    # Only host is signed, so a plain link or <audio src> can follow the URL;
    # the Range header an <audio> element sends for seeking is not signed.
    assert query["X-Amz-SignedHeaders"] == ["host"]
