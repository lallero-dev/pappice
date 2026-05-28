package store

var (
	ticketStatuses            = []string{"new", "assigned", "resolved", "rejected"}
	ticketSeverities          = []string{"support", "question", "incident", "task"}
	ticketPriorities          = []string{"low", "normal", "high", "urgent"}
	globalRoles               = []string{"admin", "staff", "customer"}
	productRoles              = []string{"owner", "agent", "customer", "viewer"}
	ticketSources             = []string{"staff", "portal"}
	commentVisibilities       = []string{"public", "internal"}
	webhookEvents             = []string{"ticket.created", "ticket.updated", "ticket.commented", "ticket.assigned"}
	defaultWebhookEvents      = []string{"ticket.created", "ticket.updated", "ticket.commented"}
	emailEvents               = appendStrings(webhookEvents, "account.setup", "account.reset", "email.test")
	emailNotificationStatuses = []string{"pending", "sending", "sent", "failed"}
	accountLinkPurposes       = []string{"setup", "reset"}

	validStatuses                  = stringSet(ticketStatuses)
	validSeverities                = stringSet(ticketSeverities)
	validPriorities                = stringSet(ticketPriorities)
	validGlobalRoles               = stringSet(globalRoles)
	validProductRoles              = stringSet(productRoles)
	validTicketSources             = stringSet(ticketSources)
	validCommentVisibility         = stringSet(commentVisibilities)
	validEvents                    = stringSet(appendStrings(webhookEvents, "*"))
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
		Username:              user.Username,
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
