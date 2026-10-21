# VPC with public and private subnets across the availability zones.

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 6.7"

  name = "${var.project_name}-vpc"
  cidr = var.vpc_cidr

  azs             = var.availability_zones
  private_subnets = var.private_subnet_cidrs
  public_subnets  = var.public_subnet_cidrs

  single_nat_gateway = var.enable_nat_gateway
  enable_nat_gateway = var.enable_nat_gateway
}

# Gateway endpoints route S3 and DynamoDB traffic from the private subnets
# straight to those services instead of through the NAT gateway, which bills
# per GB: the worker's video downloads and uploads would otherwise pass through
# it. Gateway endpoints have no charge of their own. They add routes to the
# private route tables, so nothing in the services' code or config changes.
#
# No endpoint policy, so the default (full access) applies and IAM alone
# decides what each task may do. A policy limited to this project's buckets
# would also block ECR image pulls, whose layers come from an AWS-owned bucket.
module "vpc_endpoints" {
  source  = "terraform-aws-modules/vpc/aws//modules/vpc-endpoints"
  version = "~> 6.7"

  vpc_id = module.vpc.vpc_id

  endpoints = {
    s3 = {
      service         = "s3"
      service_type    = "Gateway"
      route_table_ids = module.vpc.private_route_table_ids
    }
    dynamodb = {
      service         = "dynamodb"
      service_type    = "Gateway"
      route_table_ids = module.vpc.private_route_table_ids
    }
  }
}
