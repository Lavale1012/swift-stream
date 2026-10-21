# Task roles: the AWS credentials the worker's and the API's own code get
# through the SDK's default credential chain. Each service gets only the
# actions its code calls, on only the resources it uses.
#
# Execution roles (pulling the image from ECR, writing logs) are separate and
# are left to the ECS service module when the services are added. Pass these
# roles to it with create_tasks_iam_role = false and tasks_iam_role_arn.

data "aws_caller_identity" "current" {}

locals {
  # Also used by the job queue's policy in sqs.tf.
  account_id = data.aws_caller_identity.current.account_id

  # Every object the pipeline reads or writes lives under uploads/.
  raw_upload_objects = "${module.s3_raw_uploads.s3_bucket_arn}/uploads/*"
  processed_objects  = "${module.s3_processed.s3_bucket_arn}/uploads/*"
}

# Only ECS tasks in this account and region may take on these roles. The
# conditions stop another account's ECS from being tricked into using them.
data "aws_iam_policy_document" "ecs_tasks_assume_role" {
  statement {
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [local.account_id]
    }

    condition {
      test     = "ArnLike"
      variable = "aws:SourceArn"
      values   = ["arn:aws:ecs:${var.aws_region}:${local.account_id}:*"]
    }
  }
}

################################################################################
# Worker
################################################################################

resource "aws_iam_role" "worker_task" {
  name               = "${var.project_name}-${var.environment}-worker-task"
  description        = "Credentials for the worker's code: consume jobs, read uploads, write output, update job state."
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json
}

data "aws_iam_policy_document" "worker_task" {
  # worker/consumer: long-poll, delete on success, heartbeat and release.
  statement {
    sid = "ConsumeJobQueue"
    actions = [
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:ChangeMessageVisibility",
    ]
    resources = [module.job_queue.queue_arn]
  }

  # worker/job: download the source video.
  statement {
    sid       = "ReadUploads"
    actions   = ["s3:GetObject"]
    resources = [local.raw_upload_objects]
  }

  # Without list permission, S3 answers GetObject on a missing key with 403
  # AccessDenied instead of 404 NoSuchKey, so an expired upload would look
  # like a permissions problem in the worker's logs. The raw bucket holds only
  # uploads, so listing it shows nothing else. (A condition on s3:prefix would
  # not help: that key is absent from GetObject requests, so it never matches.)
  statement {
    sid       = "ListUploads"
    actions   = ["s3:ListBucket"]
    resources = [module.s3_raw_uploads.s3_bucket_arn]
  }

  # worker/job: upload the result next to the source's key.
  statement {
    sid       = "WriteOutput"
    actions   = ["s3:PutObject"]
    resources = [local.processed_objects]
  }

  # worker/jobstate: Start, Complete and Fail are all UpdateItem.
  statement {
    sid       = "UpdateJobState"
    actions   = ["dynamodb:UpdateItem"]
    resources = [module.jobs_table.dynamodb_table_arn]
  }
}

resource "aws_iam_role_policy" "worker_task" {
  name   = "worker-task"
  role   = aws_iam_role.worker_task.id
  policy = data.aws_iam_policy_document.worker_task.json
}

################################################################################
# API
################################################################################

resource "aws_iam_role" "api_task" {
  name               = "${var.project_name}-${var.environment}-api-task"
  description        = "Credentials for the API's code: presign uploads and audio links, create and read job records."
  assume_role_policy = data.aws_iam_policy_document.ecs_tasks_assume_role.json
}

data "aws_iam_policy_document" "api_task" {
  # A presigned URL carries the permissions of whoever signed it. The API never
  # uploads a video itself, but without this the URLs it hands out fail.
  statement {
    sid       = "PresignUploads"
    actions   = ["s3:PutObject"]
    resources = [local.raw_upload_objects]
  }

  # Presigned play and download links for the extracted audio.
  statement {
    sid       = "PresignOutputLinks"
    actions   = ["s3:GetObject"]
    resources = [local.processed_objects]
  }

  # server/app/jobs.py: PutItem creates the record at upload time; GetItem
  # reads it to find the job's output.
  statement {
    sid = "JobRecords"
    actions = [
      "dynamodb:PutItem",
      "dynamodb:GetItem",
    ]
    resources = [module.jobs_table.dynamodb_table_arn]
  }
}

resource "aws_iam_role_policy" "api_task" {
  name   = "api-task"
  role   = aws_iam_role.api_task.id
  policy = data.aws_iam_policy_document.api_task.json
}
