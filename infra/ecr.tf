# One image repository per service: worker/Dockerfile and server/Dockerfile.
# Images are pushed by tag (the git commit SHA) and the ECS task definitions
# pull them through their execution roles.
module "ecr" {
  source  = "terraform-aws-modules/ecr/aws"
  version = "~> 3.2"

  for_each = toset(["worker", "api"])

  repository_name = "${var.project_name}-${var.environment}-${each.key}"

  # A pushed tag can never point at a different image later, so a task
  # definition that names a tag always runs the same build. It also means no
  # "latest": every push needs a new tag.
  repository_image_tag_mutability = "IMMUTABLE"

  # Basic scanning: checks OS packages for known CVEs on every push, at no cost.
  repository_image_scan_on_push = true

  repository_force_delete = var.force_delete_repositories

  # No repository policy. Within one account, IAM policies alone decide who
  # can pull and push (ECS execution roles for pulls, your user or a CI role
  # for pushes); a repository policy is only needed for other accounts.
  create_repository_policy = false
  attach_repository_policy = false

  # Untagged images (layers left behind by failed or replaced pushes) go after
  # a day; of the rest, only the newest ecr_images_to_keep stay, which is
  # enough to roll back a few deploys. ECR bills storage per GB-month.
  create_lifecycle_policy = true
  repository_lifecycle_policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after a day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep only the newest images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = var.ecr_images_to_keep
        }
        action = { type = "expire" }
      },
    ]
  })
}
