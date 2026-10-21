terraform {
  # 1.10 is the first release whose S3 backend can lock state without a
  # DynamoDB table (use_lockfile).
  required_version = ">= 1.10"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.44" # the newest minimum among the modules used here
    }
  }

  # State lives in the bucket created by bootstrap/. The bucket name is left
  # out so it is not hard-coded; pass it once when initialising:
  #   terraform init -backend-config="bucket=<state bucket name>"
  # Backend settings cannot use variables, so the region is written out here.
  backend "s3" {
    key          = "swift-stream/terraform.tfstate"
    region       = "us-east-1"
    use_lockfile = true
    encrypt      = true
  }
}

provider "aws" {
  region = var.aws_region

  # Applied to every resource that supports tags, including those inside modules.
  default_tags {
    tags = merge(var.tags, {
      Project     = var.project_name
      Environment = var.environment
      Terraform   = "true"
    })
  }
}
