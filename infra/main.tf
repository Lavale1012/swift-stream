module "vpc" {
  source = "terraform-aws-modules/vpc/aws"

  name = "${var.project_name}-vpc"
  cidr = var.vpc_cidr

  azs             = var.availability_zones
  private_subnets = var.private_subnet_cidrs
  public_subnets  = var.public_subnet_cidrs

  enable_nat_gateway = var.enable_nat_gateway

  tags = merge(var.tags, {
    Terraform   = "true"
    Environment = var.environment
  })
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

  tags = merge(var.tags, {
    Terraform   = "true"
    Environment = var.environment
  })
}

# Source videos uploaded by the browser through presigned URLs (the API's UPLOAD_BUCKET).
module "s3_raw_uploads" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "~> 5.17"

  bucket = var.raw_uploads_bucket_name
  acl    = "private"

  control_object_ownership = true
  object_ownership         = "ObjectWriter"

  force_destroy = var.force_destroy_buckets

  # Lets the browser PUT to presigned URLs. The URL's signature still decides
  # what can be uploaded; CORS only decides which pages may send the request.
  cors_rule = [{
    allowed_methods = ["PUT", "GET"]
    allowed_origins = var.upload_cors_allowed_origins
    allowed_headers = ["*"]
    max_age_seconds = 3000
  }]

  versioning = {
    enabled = true
  }
}

# Pipeline output: HLS renditions, audio, subtitles (the worker's OUTPUT_BUCKET).
module "s3_processed" {
  source  = "terraform-aws-modules/s3-bucket/aws"
  version = "~> 5.17"

  bucket = var.processed_bucket_name
  acl    = "private"

  control_object_ownership = true
  object_ownership         = "ObjectWriter"

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
  acl    = "private"

  control_object_ownership = true
  object_ownership         = "ObjectWriter"

  force_destroy = var.force_destroy_buckets

  versioning = {
    enabled = true
  }
}
