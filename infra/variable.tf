variable "aws_region" {
  description = "AWS region for every resource. The state backend's region is set separately in versions.tf."
  type        = string
  default     = "us-east-1"
}

variable "project_name" {
  description = "Prefix for resource names, such as the VPC's name."
  type        = string
  default     = "swift-stream"
}

variable "environment" {
  description = "Deployment environment, applied as the Environment tag."
  type        = string
  default     = "dev"
}

variable "vpc_cidr" {
  description = "IPv4 CIDR block for the VPC. Every subnet CIDR must fall inside it."
  type        = string
  default     = "10.0.0.0/16"

  validation {
    condition     = can(cidrhost(var.vpc_cidr, 0))
    error_message = "vpc_cidr must be a valid IPv4 CIDR block, such as 10.0.0.0/16."
  }
}

variable "availability_zones" {
  description = "Availability zones to spread subnets across, such as [\"us-east-1a\", \"us-east-1b\"]. An Application Load Balancer needs at least two."
  type        = list(string)

  validation {
    condition     = length(var.availability_zones) >= 2
    error_message = "Give at least two availability zones; an Application Load Balancer requires subnets in two or more."
  }
}

variable "private_subnet_cidrs" {
  description = "CIDR blocks for the private subnets, one per availability zone, in the same order."
  type        = list(string)
  default     = ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"]

  validation {
    condition     = length(var.private_subnet_cidrs) == length(var.availability_zones)
    error_message = "private_subnet_cidrs needs exactly one CIDR per availability zone."
  }
}

variable "public_subnet_cidrs" {
  description = "CIDR blocks for the public subnets, one per availability zone, in the same order."
  type        = list(string)
  default     = ["10.0.101.0/24", "10.0.102.0/24", "10.0.103.0/24"]

  validation {
    condition     = length(var.public_subnet_cidrs) == length(var.availability_zones)
    error_message = "public_subnet_cidrs needs exactly one CIDR per availability zone."
  }
}

variable "enable_nat_gateway" {
  description = "Create NAT gateways so private subnets can reach the internet. Each NAT gateway bills hourly plus per GB processed."
  type        = bool
  default     = true
}

variable "enable_container_insights" {
  description = "Turn on CloudWatch Container Insights for the ECS cluster. Its metrics are billed as custom CloudWatch metrics."
  type        = bool
  default     = false
}

variable "log_retention_days" {
  description = "Days to keep CloudWatch log events."
  type        = number
  default     = 14
}

variable "raw_uploads_bucket_name" {
  description = "Name of the bucket that receives uploaded source videos. Must be globally unique."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.raw_uploads_bucket_name))
    error_message = "Bucket names are 3-63 characters of lowercase letters, digits, dots, and hyphens, starting and ending with a letter or digit."
  }
}

variable "processed_bucket_name" {
  description = "Name of the bucket that holds pipeline output (HLS renditions, audio, subtitles). Must be globally unique."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.processed_bucket_name))
    error_message = "Bucket names are 3-63 characters of lowercase letters, digits, dots, and hyphens, starting and ending with a letter or digit."
  }
}

variable "web_bucket_name" {
  description = "Name of the bucket that holds the built frontend. Must be globally unique."
  type        = string

  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.web_bucket_name))
    error_message = "Bucket names are 3-63 characters of lowercase letters, digits, dots, and hyphens, starting and ending with a letter or digit."
  }
}

variable "upload_cors_allowed_origins" {
  description = "Origins (scheme, host, and port) whose pages may upload to the raw uploads bucket from the browser."
  type        = list(string)
  default     = ["http://localhost:5173"]
}

variable "upload_expiration_days" {
  description = "Days after which uploaded source videos are deleted from the raw uploads bucket. A job, or a retry of it, that starts later than this fails because its source is gone."
  type        = number
  default     = 7

  validation {
    condition     = var.upload_expiration_days >= 1 && floor(var.upload_expiration_days) == var.upload_expiration_days
    error_message = "upload_expiration_days must be a whole number of days, at least 1."
  }
}

variable "force_destroy_buckets" {
  description = "Let terraform destroy delete the S3 buckets even when they still hold objects, deleting the objects too. Keep this off outside dev."
  type        = bool
  default     = true
}

variable "tags" {
  description = "Extra tags added to every resource, on top of Project, Environment and Terraform."
  type        = map(string)
  default     = {}
}
