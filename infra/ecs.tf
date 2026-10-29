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

  services = {
    # Long-polls the job queue and processes one video at a time (worker/).
    # Runs in the private subnets with no inbound traffic.
    worker = {
      # 0 until an image is pushed and worker_image_tag is set. Queue-depth
      # autoscaling replaces this once it is added.
      desired_count = var.worker_desired_count

      # The module defaults to target tracking on CPU and memory with at
      # least one task always running. CPU is the wrong signal for a queue
      # worker, and an idle task bills around the clock.
      enable_autoscaling = false

      cpu    = var.worker_cpu
      memory = var.worker_memory

      # Images are built with --platform linux/arm64 (Graviton, about 20%
      # cheaper than x86 on Fargate). An x86 image here fails at start with
      # "exec format error".
      runtime_platform = {
        cpu_architecture        = "ARM64"
        operating_system_family = "LINUX"
      }

      subnet_ids       = module.vpc.private_subnets
      assign_public_ip = false

      # Every AWS endpoint the worker uses (ECR, SQS, S3, DynamoDB, STS,
      # CloudWatch Logs) is HTTPS. The module adds no egress by default, which
      # would leave the task unable to pull its image. DNS goes to the VPC
      # resolver, which security groups do not filter.
      security_group_egress_rules = {
        https = {
          description = "HTTPS to AWS APIs"
          ip_protocol = "tcp"
          from_port   = 443
          to_port     = 443
          cidr_ipv4   = "0.0.0.0/0"
        }
      }

      # The role in iam.tf; the module still creates the execution role,
      # which pulls the image and writes logs.
      create_tasks_iam_role = false
      tasks_iam_role_arn    = aws_iam_role.worker_task.arn

      container_definitions = {
        worker = {
          image     = "${module.ecr["worker"].repository_url}:${coalesce(var.worker_image_tag, "unset")}"
          essential = true

          environment = [
            { name = "AWS_REGION", value = var.aws_region },
            { name = "JOB_QUEUE_URL", value = module.job_queue.queue_url },
            { name = "OUTPUT_BUCKET", value = module.s3_processed.s3_bucket_id },
            { name = "JOBS_TABLE", value = module.jobs_table.dynamodb_table_id },
          ]

          # The module defaults to a read-only root filesystem, but the worker
          # writes each job's files under WORK_DIR (/tmp/swift-stream-worker,
          # set in worker/Dockerfile), which lives in the task's ephemeral
          # storage (20 GiB).
          readonlyRootFilesystem = false

          # On SIGTERM (deploys, scale-in, Spot interruption) the worker only
          # releases its message, which takes seconds. ECS sends SIGKILL after
          # this many seconds.
          stopTimeout = 30

          cloudwatch_log_group_retention_in_days = var.log_retention_days
        }
      }
    }
  }
}
