package store

import "slices"

var (
	ticketStatuses            = []string{"open", "closed"}
	ticketPriorities          = []string{"low", "normal", "high", "urgent"}
	globalRoles               = []string{"admin", "staff", "customer"}
	productRoles              = []string{"manager", "staff", "customer", "viewer"}
	commentVisibilities       = []string{"public", "internal"}
	webhookEvents             = []string{"ticket.created", "ticket.updated", "ticket.commented", "ticket.assigned"}
	defaultWebhookEvents      = []string{"ticket.created", "ticket.updated", "ticket.commented"}
	auditEvents               = []string{"password.changed", "setup.completed", "product.created", "product.updated", "product.deleted", "product_member.upserted", "product_member.removed", "ticket.deleted", "user.created", "user.updated", "user.deleted", "user.password_reset_requested", "api_token.created", "api_token.deleted", "webhook.created", "webhook.updated", "webhook.deleted", "webhook.secret_rotated", "email_notification.retried", "email_notification.test_queued"}
	emailEvents               = append(slices.Clone(webhookEvents), "account.setup", "account.reset", "email.test")
	emailNotificationStatuses = []string{"pending", "sending", "sent", "failed"}
	accountLinkPurposes       = []string{"setup", "reset"}
	domainEvents              = append(slices.Clone(webhookEvents), auditEvents...)
)

func Statuses() []string {
	return slices.Clone(ticketStatuses)
}

func Priorities() []string {
	return slices.Clone(ticketPriorities)
}

func Roles() []string {
	return slices.Clone(globalRoles)
}

func ProductRoles() []string {
	return slices.Clone(productRoles)
}

func Events() []string {
	return slices.Clone(webhookEvents)
}

func ToPublicUser(user User) PublicUser {
	return PublicUser{
		ID:                    user.ID,
		DisplayName:           user.DisplayName,
		Email:                 user.Email,
		Role:                  normalizeGlobalRole(user.Role),
		Disabled:              user.Disabled,
		PasswordResetRequired: user.PasswordResetRequired,
		CreatedAt:             user.CreatedAt,
		UpdatedAt:             user.UpdatedAt,
	}
}

func ToPublicWebhook(hook Webhook) PublicWebhook {
	return PublicWebhook{
		ID:              hook.ID,
		ProductID:       hook.ProductID,
		Name:            hook.Name,
		URL:             hook.URL,
		Events:          append([]string(nil), hook.Events...),
		Enabled:         hook.Enabled,
		HasSecret:       hook.Secret != "",
		CreatedAt:       hook.CreatedAt,
		UpdatedAt:       hook.UpdatedAt,
		LastStatus:      hook.LastStatus,
		LastError:       hook.LastError,
		LastDeliveredAt: hook.LastDeliveredAt,
	}
}
