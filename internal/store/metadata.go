package store

func Statuses() []string {
	return []string{"new", "assigned", "resolved", "rejected"}
}

func Priorities() []string {
	return []string{"low", "normal", "high", "urgent"}
}

func Roles() []string {
	return []string{"admin", "staff", "customer"}
}

func ProjectRoles() []string {
	return []string{"owner", "agent", "customer", "viewer"}
}

func Events() []string {
	return []string{"ticket.created", "ticket.updated", "ticket.commented", "ticket.assigned"}
}

func ToPublicUser(user User) PublicUser {
	return PublicUser{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Email:       user.Email,
		Role:        normalizeGlobalRole(user.Role),
		Disabled:    user.Disabled,
		CreatedAt:   user.CreatedAt,
		UpdatedAt:   user.UpdatedAt,
	}
}

func ToPublicWebhook(hook Webhook) PublicWebhook {
	return PublicWebhook{
		ID:              hook.ID,
		ProjectID:       hook.ProjectID,
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
