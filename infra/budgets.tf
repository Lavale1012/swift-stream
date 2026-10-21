# Emails when the month's spend passes 80% of the budget, and when AWS
# forecasts that it will pass 100% by the end of the month. The forecast
# usually warns first, while there is still time to stop something.
#
# The budget covers the whole account, not only this project: filtering by
# the Project tag would need that tag activated as a cost allocation tag in
# the Billing console first. Alerts only notify; nothing is stopped.
resource "aws_budgets_budget" "monthly" {
  name         = "${var.project_name}-${var.environment}-monthly"
  budget_type  = "COST"
  limit_amount = tostring(var.monthly_budget_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = var.budget_alert_emails
  }

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "FORECASTED"
    subscriber_email_addresses = var.budget_alert_emails
  }
}
