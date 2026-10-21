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
