"""DynamoJobStore against botocore's Stubber, which checks each request's
parameters against the DynamoDB API model without a network call."""

import pytest
from botocore.exceptions import ClientError
from botocore.stub import ANY, Stubber

from app.jobs import DynamoJobStore, Job, dynamodb_client

JOB_ID = "0123456789abcdef0123456789abcdef"
TABLE = "jobs"
GET_PARAMS = {
    "TableName": TABLE,
    "Key": {"job_id": {"S": JOB_ID}},
    "ConsistentRead": True,
    "ProjectionExpression": "#status, output_key",
    "ExpressionAttributeNames": {"#status": "status"},
}


@pytest.fixture
def dynamodb(aws_session):
    client = dynamodb_client(aws_session)
    with Stubber(client) as stub:
        yield client, stub
        stub.assert_no_pending_responses()


def test_create_writes_pending_job(dynamodb):
    client, stub = dynamodb
    stub.add_response(
        "put_item",
        {},
        {
            "TableName": TABLE,
            "Item": {
                "job_id": {"S": JOB_ID},
                "status": {"S": "pending"},
                "source_key": {"S": f"uploads/{JOB_ID}/source.mp4"},
                "created_at": ANY,
                "updated_at": ANY,
            },
            "ConditionExpression": "attribute_not_exists(job_id)",
        },
    )
    DynamoJobStore(client, TABLE).create(JOB_ID, f"uploads/{JOB_ID}/source.mp4")


def test_create_does_not_overwrite_a_job(dynamodb):
    client, stub = dynamodb
    stub.add_client_error("put_item", service_error_code="ConditionalCheckFailedException")
    with pytest.raises(ClientError):
        DynamoJobStore(client, TABLE).create(JOB_ID, f"uploads/{JOB_ID}/source.mp4")


@pytest.mark.parametrize(
    ("item", "want"),
    [
        pytest.param(None, None, id="unknown"),
        pytest.param({"status": {"S": "processing"}}, Job("processing", None), id="processing"),
        pytest.param(
            {"status": {"S": "completed"}, "output_key": {"S": "uploads/x/audio.mp3"}},
            Job("completed", "uploads/x/audio.mp3"),
            id="completed",
        ),
    ],
)
def test_get(dynamodb, item, want):
    client, stub = dynamodb
    stub.add_response("get_item", {"Item": item} if item else {}, GET_PARAMS)
    assert DynamoJobStore(client, TABLE).get(JOB_ID) == want
