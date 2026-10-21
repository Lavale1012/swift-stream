# Creates the bucket that holds the main configuration's Terraform state.
#
# It runs once, before the main configuration, and keeps its own small state
# in a local file (bootstrap/terraform.tfstate, which .gitignore excludes).
# If that file is lost, the bucket itself is safe; bring it back under
# Terraform with `terraform import aws_s3_bucket.state <bucket name>`.

terraform {
  required_version = ">= 1.10"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.44"
    }
  }
}

provider "aws" {
  region = var.aws_region

  default_tags {
    tags = {
      Project   = var.project_name
      Terraform = "true"
    }
  }
}

variable "aws_region" {
  description = "Region for the state bucket. Must match the region in ../versions.tf's backend block."
  type        = string
  default     = "us-east-1"
}

variable "project_name" {
  description = "Value of the Project tag."
  type        = string
  default     = "swift-stream"
}

variable "state_bucket_name" {
  description = "Name of the state bucket. Must be globally unique."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.state_bucket_name))
    error_message = "Bucket names are 3-63 characters of lowercase letters, digits, dots, and hyphens, starting and ending with a letter or digit."
  }
}

resource "aws_s3_bucket" "state" {
  bucket = var.state_bucket_name

  # Losing this bucket loses track of every resource the main configuration
  # manages, so Terraform refuses to destroy it.
  lifecycle {
    prevent_destroy = true
  }
}

# Every state write is kept, so a bad apply can be rolled back.
resource "aws_s3_bucket_versioning" "state" {
  bucket = aws_s3_bucket.state.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "state" {
  bucket = aws_s3_bucket.state.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_ownership_controls" "state" {
  bucket = aws_s3_bucket.state.id

  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_public_access_block" "state" {
  bucket = aws_s3_bucket.state.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

output "state_bucket_name" {
  description = "Pass this to the main configuration: terraform init -backend-config=\"bucket=<name>\"."
  value       = aws_s3_bucket.state.bucket
}
