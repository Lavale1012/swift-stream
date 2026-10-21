# Jobs for the worker. S3 sends a message here for every upload; the worker
# receives, processes and deletes them (worker/consumer).
module "job_queue" {
  source  = "terraform-aws-modules/sqs/aws"
  version = "~> 5.2"

  name = "${var.project_name}-${var.environment}-jobs"

  # Matches visibilityTimeout in worker/consumer. The worker sets it on each
  # receive and renews it while a job runs, so this is the default for anything
  # else that reads the queue.
  visibility_timeout_seconds = 120
  receive_wait_time_seconds  = 20 # long polling
  message_retention_seconds  = var.job_retention_days * 86400
  sqs_managed_sse_enabled    = true

  # After job_max_receive_count failed deliveries, SQS moves the message to
  # the dead-letter queue. The module defaults to 5, which is the worker's own
  # give-up limit (maxAttempts in worker/main.go); the worker would delete the
  # message first and nothing would ever reach the DLQ, so the limit is set
  # below it.
  create_dlq = true
  redrive_policy = {
    maxReceiveCount = var.job_max_receive_count
  }
  dlq_message_retention_seconds = var.dlq_retention_days * 86400
  dlq_sqs_managed_sse_enabled   = true

  # Lets S3 send upload events, from the raw uploads bucket in this account
  # only. Bucket ARNs carry no account ID, so without aws:SourceAccount a
  # bucket of the same name in another account could send here if ours were
  # ever deleted and the name reused.
  create_queue_policy = true
  queue_policy_statements = {
    s3_uploads = {
      sid     = "AllowRawUploadsBucketEvents"
      actions = ["sqs:SendMessage"]
      principals = [{
        type        = "Service"
        identifiers = ["s3.amazonaws.com"]
      }]
      condition = [
        {
          test     = "ArnEquals"
          variable = "aws:SourceArn"
          values   = [module.s3_raw_uploads.s3_bucket_arn]
        },
        {
          test     = "StringEquals"
          variable = "aws:SourceAccount"
          values   = [local.account_id]
        },
      ]
    }
  }
}

# Sends an event to the job queue whenever a video lands under uploads/. Only
# the API's presigned uploads write there (uploads/<job id>/source.<ext>).
module "raw_uploads_notifications" {
  source  = "terraform-aws-modules/s3-bucket/aws//modules/notification"
  version = "~> 5.17"

  bucket     = module.s3_raw_uploads.s3_bucket_id
  bucket_arn = module.s3_raw_uploads.s3_bucket_arn

  sqs_notifications = {
    uploads = {
      queue_arn     = module.job_queue.queue_arn
      queue_id      = module.job_queue.queue_url
      events        = ["s3:ObjectCreated:*"]
      filter_prefix = "uploads/"
    }
  }

  # The queue policy above already lets S3 send, with a stricter condition
  # than the one this module would write.
  create_sqs_policy = false

  # S3 sends a test message when the notification is saved and rejects it if
  # the queue policy does not allow S3 yet, so the policy must exist first.
  depends_on = [module.job_queue]
}
