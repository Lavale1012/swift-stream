# One item per job, keyed by the job ID from the upload key
# (uploads/<job id>/<file>). The API creates it at upload time and the worker
# updates it as the job runs; worker/jobstate documents the attributes.
module "jobs_table" {
  source  = "terraform-aws-modules/dynamodb-table/aws"
  version = "~> 5.5"

  name         = "${var.project_name}-${var.environment}-jobs"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "job_id"

  attributes = [{
    name = "job_id"
    type = "S"
  }]
}
