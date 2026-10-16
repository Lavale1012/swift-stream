# One item per job, keyed by the job ID from the upload key
# (uploads/<job id>/<file>). The worker writes it; the attributes are
# documented in worker/jobstate.
resource "aws_dynamodb_table" "jobs" {
  name         = "${var.project}-jobs"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "job_id"

  attribute {
    name = "job_id"
    type = "S"
  }
}
