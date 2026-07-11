package store

var (
	ticketStatuses            = []string{"new", "assigned", "resolved", "rejected"}
	ticketPriorities          = []string{"low", "normal", "high", "urgent"}
	globalRoles               = []string{"admin", "staff", "customer"}
	productRoles              = []string{"manager", "staff", "customer", "viewer"}
	commentVisibilities       = []string{"public", "internal"}
	webhookEvents             = []string{"ticket.created", "ticket.updated", "ticket.commented", "ticket.assigned"}
	defaultWebhookEvents      = []string{"ticket.created", "ticket.updated", "ticket.commented"}
	auditEvents               = []string{"password.changed", "setup.completed", "product.created", "product.updated", "product.deleted", "product_member.upserted", "product_member.removed", "ticket.deleted", "user.created", "user.updated", "user.deleted", "user.password_reset_requested", "api_token.created", "api_token.deleted", "webhook.created", "webhook.updated", "webhook.deleted", "webhook.secret_rotated", "email_notification.retried", "email_notification.test_queued"}
	emailEvents               = appendStrings(webhookEvents, "account.setup", "account.reset", "email.test")
	emailNotificationStatuses = []string{"pending", "sending", "sent", "failed"}
	accountLinkPurposes       = []string{"setup", "reset"}

	validStatuses                  = stringSet(ticketStatuses)
	validPriorities                = stringSet(ticketPriorities)
	validGlobalRoles               = stringSet(globalRoles)
	validProductRoles              = stringSet(productRoles)
	validCommentVisibility         = stringSet(commentVisibilities)
	validEvents                    = stringSet(appendStrings(webhookEvents, "*"))
	validDomainEvents              = stringSet(appendStrings(webhookEvents, auditEvents...))
	validEmailEvents               = stringSet(emailEvents)
	validEmailNotificationStatuses = stringSet(emailNotificationStatuses)
	validAccountLinkPurposes       = stringSet(accountLinkPurposes)
)

func Statuses() []string {
	return cloneStrings(ticketStatuses)
}

func Priorities() []string {
	return cloneStrings(ticketPriorities)
}

func Roles() []string {
	return cloneStrings(globalRoles)
}

func ProductRoles() []string {
	return cloneStrings(productRoles)
}

func Events() []string {
	return cloneStrings(webhookEvents)
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}

func appendStrings(values []string, extras ...string) []string {
	result := cloneStrings(values)
	return append(result, extras...)
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
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
