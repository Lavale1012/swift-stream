# Bucket names feed the services' environment variables; the network and
# cluster outputs are what running a task or service needs.

output "aws_region" {
  description = "Region every resource is in."
  value       = var.aws_region
}

output "raw_uploads_bucket_name" {
  description = "Bucket that receives uploaded videos. Set as the API's UPLOAD_BUCKET."
  value       = module.s3_raw_uploads.s3_bucket_id
}

output "processed_bucket_name" {
  description = "Bucket for pipeline output. Set as OUTPUT_BUCKET for both the worker and the API."
  value       = module.s3_processed.s3_bucket_id
}

output "web_bucket_name" {
  description = "Bucket for the built client. Upload with: aws s3 sync client/dist s3://<name>"
  value       = module.s3_web.s3_bucket_id
}

output "ecs_cluster_name" {
  description = "ECS cluster that runs the worker and the API."
  value       = module.ecs.cluster_name
}

output "vpc_id" {
  description = "VPC the services run in."
  value       = module.vpc.vpc_id
}

output "private_subnet_ids" {
  description = "Subnets for tasks that should not be reachable from the internet, such as the worker."
  value       = module.vpc.private_subnets
}

output "public_subnet_ids" {
  description = "Subnets for internet-facing resources, such as a load balancer in front of the API."
  value       = module.vpc.public_subnets
}

output "job_queue_url" {
  description = "Queue the worker consumes. Set as the worker's JOB_QUEUE_URL."
  value       = module.job_queue.queue_url
}

output "job_queue_arn" {
  description = "ARN of the job queue, for IAM policies."
  value       = module.job_queue.queue_arn
}

output "job_dlq_url" {
  description = "Dead-letter queue where job messages land after job_max_receive_count failed deliveries."
  value       = module.job_queue.dead_letter_queue_url
}

output "jobs_table_name" {
  description = "Job records. Set as JOBS_TABLE for both the worker and the API."
  value       = module.jobs_table.dynamodb_table_id
}

output "jobs_table_arn" {
  description = "ARN of the jobs table, for IAM policies."
  value       = module.jobs_table.dynamodb_table_arn
}

output "worker_task_role_arn" {
  description = "Task role for the worker's ECS service (tasks_iam_role_arn)."
  value       = aws_iam_role.worker_task.arn
}

output "api_task_role_arn" {
  description = "Task role for the API's ECS service (tasks_iam_role_arn)."
  value       = aws_iam_role.api_task.arn
}

