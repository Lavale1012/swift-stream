"""Job records in the DynamoDB table the worker also writes (JOBS_TABLE).

One item per job, keyed by job_id. The API creates it with status "pending"
when it hands out the upload URL; from then on the worker owns it (see
worker/jobstate for the attributes it sets). The worker's Start overwrites
any item that has not completed, so it takes over a pending item unchanged.

    pending -> processing -> completed
                         \\-> failed -> processing (SQS retries the job)
"""

from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Protocol

import boto3
from botocore.config import Config

from app.aws import TIMEOUTS

STATUS_PENDING = "pending"
STATUS_PROCESSING = "processing"
STATUS_COMPLETED = "completed"
STATUS_FAILED = "failed"


@dataclass(frozen=True)
class Job:
    status: str
    # Where the worker wrote its output in the output bucket; set once the
    # job completes.
    output_key: str | None


class JobStore(Protocol):
    """What the routes need from the jobs table, so tests can pass a fake."""

    def create(self, job_id: str, source_key: str) -> None: ...

    def get(self, job_id: str) -> Job | None: ...


class DynamoJobStore:
    def __init__(self, client, table: str) -> None:
        self._client = client
        self._table = table

    def create(self, job_id: str, source_key: str) -> None:
        # Times are RFC 3339 in UTC, the format the worker writes.
        now = datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
        self._client.put_item(
            TableName=self._table,
            Item={
                "job_id": {"S": job_id},
                "status": {"S": STATUS_PENDING},
                "source_key": {"S": source_key},
                "created_at": {"S": now},
                "updated_at": {"S": now},
            },
            # Job IDs are random, so this only fails on a collision, which
            # must not overwrite another job.
            ConditionExpression="attribute_not_exists(job_id)",
        )

    def get(self, job_id: str) -> Job | None:
        response = self._client.get_item(
            TableName=self._table,
            Key={"job_id": {"S": job_id}},
            # A job the worker has just completed must not read as still
            # processing, which an eventually consistent read could return.
            ConsistentRead=True,
            # status is a DynamoDB reserved word.
            ProjectionExpression="#status, output_key",
            ExpressionAttributeNames={"#status": "status"},
        )
        item = response.get("Item")
        if item is None:
            return None
        return Job(status=item["status"]["S"], output_key=item.get("output_key", {}).get("S"))


def dynamodb_client(session: boto3.Session):
    return session.client("dynamodb", config=Config(**TIMEOUTS))
