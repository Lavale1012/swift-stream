module "vpc" {
  source = "terraform-aws-modules/vpc/aws"

  name = "${var.project_name}-vpc"
  cidr = var.vpc_cidr

  azs             = var.availability_zones
  private_subnets = var.private_subnet_cidrs
  public_subnets  = var.public_subnet_cidrs

  single_nat_gateway = var.enable_nat_gateway
}

module "ecs" {
  source  = "terraform-aws-modules/ecs/aws"
  version = "~> 7.6"

  cluster_name = "${var.project_name}-${var.environment}"

  cluster_setting = [{
    name  = "containerInsights"
    value = var.enable_container_insights ? "enabled" : "disabled"
  }]

  # Log group for ECS Exec sessions; task logs get their own groups per service.
  cloudwatch_log_group_retention_in_days = var.log_retention_days

  # Both are available to services; each service can set its own strategy.
  cluster_capacity_providers = ["FARGATE", "FARGATE_SPOT"]
  default_capacity_provider_strategy = {
    FARGATE = {
      weight = 1
    }
  }

  # Services (worker, API) are added here once their queue, buckets, and ECR repo exist.
  services = {}
}

# Source videos uploaded by the browser through presigned URLs (the API's UPLOAD_BUCKET).
module "s3_raw_uploads" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "~> 5.17"

  bucket = var.raw_uploads_bucket_name

  # ACLs off: the bucket owner owns every object, and access is decided by
  # IAM and bucket policies alone. AWS recommends this for new buckets.
  control_object_ownership = true
  object_ownership         = "BucketOwnerEnforced"

  force_destroy = var.force_destroy_buckets

  # Lets the browser PUT to presigned URLs. The URL's signature still decides
  # what can be uploaded; CORS only decides which pages may send the request.
  cors_rule = [{
    allowed_methods = ["PUT", "GET"]
    allowed_origins = var.upload_cors_allowed_origins
    allowed_headers = ["*"]
    max_age_seconds = 3000
  }]

  # Source videos are only needed while their job runs, including retries, so
  # they expire; the pipeline's output in the processed bucket is kept. With
  # versioning on, expiring a video only adds a delete marker on top of it, so
  # the hidden old version is removed a day later, and the marker left behind
  # is removed by the second rule. (S3 does not allow expiry by days and
  # marker removal in the same rule.)
  lifecycle_rule = [
    {
      id                                     = "expire-uploads"
      enabled                                = true
      filter                                 = { prefix = "uploads/" }
      expiration                             = { days = var.upload_expiration_days }
      noncurrent_version_expiration          = { noncurrent_days = 1 }
      abort_incomplete_multipart_upload_days = 1
    },
    {
      id         = "remove-expired-delete-markers"
      enabled    = true
      filter     = { prefix = "uploads/" }
      expiration = { expired_object_delete_marker = true }
    },
  ]

  versioning = {
    enabled = true
  }
}

# Pipeline output: HLS renditions, audio, subtitles (the worker's OUTPUT_BUCKET).
module "s3_processed" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "~> 5.17"

  bucket = var.processed_bucket_name

  # ACLs off: the bucket owner owns every object, and access is decided by
  # IAM and bucket policies alone. AWS recommends this for new buckets.
  control_object_ownership = true
  object_ownership         = "BucketOwnerEnforced"

  force_destroy = var.force_destroy_buckets

  versioning = {
    enabled = true
  }
}

# Built frontend (client/dist), served through CloudFront.
module "s3_web" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "~> 5.17"

  bucket = var.web_bucket_name

  # ACLs off: the bucket owner owns every object, and access is decided by
  # IAM and bucket policies alone. AWS recommends this for new buckets.
  control_object_ownership = true
  object_ownership         = "BucketOwnerEnforced"

  force_destroy = var.force_destroy_buckets

  versioning = {
    enabled = true
  }
}
