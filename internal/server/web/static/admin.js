import { request } from "./api.js";
import { formatBytes } from "./attachments.js";
import { badge, debounce, el, labelize, relativeTime } from "./components.js";
import { ADMIN_SECTIONS, DEFAULT_ADMIN_SECTION, els, fullDateFormatter, shortDateFormatter, state } from "./state.js";
import { accountLabel, accountName, isAdmin } from "./access.js";
import { copyText, emptyInline, factBlock, formField, selectOptions, showAppAlert, showError, showInlineConfirm } from "./ui.js";

let app = {};
let emailLoadRequestID = 0;
let auditLoadRequestID = 0;
const sectionLoaders = {
  accounts: loadUsers,
  tokens: loadTokens,
  webhooks: () => app.loadGlobalWebhooks(),
  email: loadEmailNotifications,
  maintenance: loadMaintenance,
  audit: loadAuditEvents
};

export function initAdmin(options) {
  app = options;
  bindAdminEvents();
}

function bindAdminEvents() {
  const runEmailSearch = debounce(() => {
    state.emailPage.q = els.emailSearchInput.value.trim();
    state.emailPage.offset = 0;
    loadEmailNotifications().catch(showError);
  }, 250);
  const runAuditSearch = debounce(() => {
    state.auditPage.q = els.auditSearchInput.value.trim();
    state.auditPage.offset = 0;
    loadAuditEvents().catch(showError);
  }, 250);

  for (const button of els.adminSectionButtons) {
    button.addEventListener("click", () => switchAdminSection(button.getAttribute("data-admin-section")).catch(showError));
  }
  els.emailSearchInput.addEventListener("input", runEmailSearch);
  els.emailStatusFilter.addEventListener("change", () => {
    state.emailPage.status = els.emailStatusFilter.value;
    state.emailPage.offset = 0;
    loadEmailNotifications().catch(showError);
  });
  els.auditSearchInput.addEventListener("input", runAuditSearch);
  els.addUserButton.addEventListener("click", () => openUserModal());
  els.createTokenButton.addEventListener("click", () => openTokenModal());
  els.sendTestEmailButton.addEventListener("click", () => openTestEmailModal());
}

function formatSeconds(seconds) {
  const value = Number(seconds || 0);
  if (!Number.isFinite(value) || value <= 0) return "-";
  if (value < 60) return `${Math.round(value)}s`;
  const minutes = Math.round(value / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  return `${hours}h`;
}

export async function loadAdmin() {
  await loadAdminSection(state.adminSection);
}

async function loadAdminSection(section) {
  state.adminSection = validAdminSection(section) ? section : DEFAULT_ADMIN_SECTION;
  renderAdminSections();
  await sectionLoaders[state.adminSection]();
}

export async function loadUsers() {
  const payload = await request("/api/users");
  state.users = payload.users || [];
  app.renderAssigneeFilter();
  renderUsers();
}

async function loadTokens() {
  const payload = await request("/api/tokens");
  state.tokens = payload.tokens || [];
  renderTokens();
}

async function loadEmailNotifications() {
  const requestID = ++emailLoadRequestID;
  const params = new URLSearchParams({
    limit: String(state.emailPage.limit),
    offset: String(state.emailPage.offset)
  });
  if (state.emailPage.status) params.set("status", state.emailPage.status);
  if (state.emailPage.q) params.set("q", state.emailPage.q);
  const payload = await request(`/api/email-notifications?${params.toString()}`);
  if (requestID !== emailLoadRequestID) return;
  state.emailNotifications = payload.notifications || [];
  state.emailPage.total = Number(payload.total || 0);
  state.emailPage.limit = Number(payload.limit || state.emailPage.limit);
  state.emailPage.offset = Number(payload.offset || 0);
  state.emailStats = payload.stats || null;
  state.emailEnabled = Boolean(payload.enabled);
  state.notificationDelaySeconds = Number(payload.notification_delay_seconds || 0);
  renderEmailNotifications();
}

async function loadAuditEvents() {
  const requestID = ++auditLoadRequestID;
  const params = new URLSearchParams({
    limit: String(state.auditPage.limit),
    offset: String(state.auditPage.offset)
  });
  if (state.auditPage.q) params.set("q", state.auditPage.q);
  const payload = await request(`/api/audit-events?${params.toString()}`);
  if (requestID !== auditLoadRequestID) return;
  state.auditEvents = payload.events || [];
  state.auditPage.total = Number(payload.total || 0);
  state.auditPage.limit = Number(payload.limit || state.auditPage.limit);
  state.auditPage.offset = Number(payload.offset || 0);
  renderAuditEvents();
}

async function loadMaintenance() {
  const payload = await request("/api/admin/maintenance");
  state.maintenance = payload;
  renderMaintenance();
}

function renderAdminSections() {
  for (const button of els.adminSectionButtons) {
    const active = button.getAttribute("data-admin-section") === state.adminSection;
    button.classList.toggle("active", active);
    button.setAttribute("aria-current", active ? "page" : "false");
  }
  for (const panel of els.adminSectionPanels) {
    panel.hidden = panel.getAttribute("data-admin-panel") !== state.adminSection;
  }
}

async function switchAdminSection(section) {
  if (!isAdmin()) return;
  state.adminSection = validAdminSection(section) ? section : DEFAULT_ADMIN_SECTION;
  if (state.view === "admin") app.syncRoute();
  await loadAdminSection(state.adminSection);
}

function validAdminSection(section) {
  return ADMIN_SECTIONS.includes(section);
}

function renderUsers() {
  els.userList.replaceChildren();
  for (const user of state.users) {
    const row = el("div", { className: "admin-row" });
    const edit = el("button", { className: "ghost-button", type: "button" }, "Edit");
    edit.addEventListener("click", () => openUserModal(user));

    row.append(
      el("div", { className: "admin-row-main" }, accountLabel(user)),
      badge(user.role, "priority-normal"),
      el("span", { className: user.disabled || user.password_reset_required ? "muted" : "status-ok" }, user.disabled ? "Disabled" : user.password_reset_required ? "Password pending" : "Active"),
      edit
    );
    els.userList.append(row);
  }
}

function renderTokens() {
  els.tokenList.replaceChildren();
  if (state.tokens.length === 0) {
    els.tokenList.append(emptyInline({
      title: "No API tokens",
      body: "Create a token when an integration needs API access.",
      actionLabel: "Create Token",
      onAction: openTokenModal
    }));
    return;
  }
  for (const token of state.tokens) {
    const row = el("div", { className: "admin-row" });
    const remove = el("button", { className: "ghost-button", type: "button" }, "Delete");
    remove.addEventListener("click", () => deleteToken(token.id).catch(showError));
    row.append(
      el("div", { className: "admin-row-main" }, `${token.name} / ${token.prefix}`),
      el("span", { className: "muted" }, token.last_used_at ? `Used ${relativeTime(token.last_used_at)}` : "Never used"),
      remove
    );
    els.tokenList.append(row);
  }
}

function renderEmailNotifications() {
  if (els.emailSearchInput && els.emailSearchInput.value !== state.emailPage.q) {
    els.emailSearchInput.value = state.emailPage.q;
  }
  if (els.emailStatusFilter && els.emailStatusFilter.value !== state.emailPage.status) {
    els.emailStatusFilter.value = state.emailPage.status;
  }
  renderEmailOverview();
  els.sendTestEmailButton.disabled = !state.emailEnabled;
  els.emailList.replaceChildren();
  if (!state.emailEnabled && state.emailNotifications.length === 0) {
    els.emailList.append(emptyInline({
      title: "Email is not configured",
      body: "Set SMTP environment variables to send no-reply notifications."
    }));
    renderPager(els.emailPager, state.emailPage, (offset) => {
      state.emailPage.offset = offset;
      loadEmailNotifications().catch(showError);
    });
    return;
  }
  if (state.emailNotifications.length === 0) {
    els.emailList.append(emptyInline({
      title: "No email notifications",
      body: "Queued, sent, and failed email notifications will appear here."
    }));
    renderPager(els.emailPager, state.emailPage, (offset) => {
      state.emailPage.offset = offset;
      loadEmailNotifications().catch(showError);
    });
    return;
  }
  for (const notification of state.emailNotifications) {
    els.emailList.append(emailNotificationRow(notification));
  }
  renderPager(els.emailPager, state.emailPage, (offset) => {
    state.emailPage.offset = offset;
    loadEmailNotifications().catch(showError);
  });
}

function renderEmailOverview() {
  const stats = state.emailStats || {};
  els.emailOverview.replaceChildren(
    emailStat("Enabled", state.emailEnabled ? "Yes" : "No"),
    emailStat("Pending", stats.pending || 0),
    emailStat("Sending", stats.sending || 0),
    emailStat("Failed", stats.failed || 0),
    emailStat("Sent", stats.sent || 0),
    emailStat("Notification delay", formatSeconds(state.notificationDelaySeconds)),
    emailStat("Last sent", stats.last_sent_at ? relativeTime(stats.last_sent_at) : "-")
  );
}

function renderMaintenance() {
  if (!els.maintenanceOverview) return;
  const info = state.maintenance || {};
  const uploads = info.uploads || state.meta.uploads || {};
  const email = info.email || {};
  const backup = info.backup || {};
  els.maintenanceOverview.replaceChildren(
    maintenanceItem("Version", info.version || "dev"),
    maintenanceItem("Started", info.started_at ? relativeTime(info.started_at) : "-"),
    maintenanceItem("Database", info.database_path || "-"),
    maintenanceItem("Uploads", info.upload_path || "-"),
    maintenanceItem("Backups", backup.path || "-"),
    maintenanceItem("Last backup", backup.latest_at ? `${relativeTime(backup.latest_at)} / ${backup.latest_name || "latest"}` : "No backups found"),
    maintenanceItem("Event retention", Number(info.domain_event_retention_seconds || 0) > 0 ? formatSeconds(Number(info.domain_event_retention_seconds || 0)) : "Disabled"),
    maintenanceItem("Upload limit", `${formatBytes(uploads.max_size_bytes || 0)} / ${uploads.max_files || 0} files`),
    maintenanceItem("Email", email.enabled ? "Enabled" : "Disabled"),
    maintenanceItem("Public URL", email.public_url || "-"),
    maintenanceItem("Notification delay", formatSeconds(Number(email.notification_delay_seconds || 0)))
  );
}

function maintenanceItem(label, value) {
  return el("div", { className: "maintenance-item" }, [
    el("span", {}, label),
    el("strong", {}, String(value))
  ]);
}

function emailStat(label, value) {
  return el("div", { className: "email-stat" }, [
    el("span", {}, label),
    el("strong", {}, String(value))
  ]);
}

function emailNotificationRow(notification) {
  const row = el("div", { className: "admin-row email-row" });
  const status = badge(notification.status, emailStatusClass(notification.status));
  const view = el("button", { className: "ghost-button", type: "button" }, "View");
  view.addEventListener("click", () => openEmailNotificationModal(notification));
  const retry = el("button", { className: "ghost-button", type: "button" }, "Retry");
  retry.hidden = notification.status !== "failed";
  retry.addEventListener("click", (event) => {
    event.stopPropagation();
    retryEmailNotification(notification.id).catch(showError);
  });
  row.append(
    el("div", { className: "admin-row-main" }, [
      el("strong", {}, notification.subject || notification.event),
      el("span", {}, `${notification.event} / ${notification.recipient_email}`)
    ]),
    status,
    el("span", { className: "muted" }, notification.last_error || emailNotificationTime(notification)),
    view,
    retry
  );
  return row;
}

function emailStatusClass(status) {
  if (status === "failed") return "priority-urgent";
  if (status === "sent") return "priority-low";
  return "priority-normal";
}

function emailNotificationTime(notification) {
  if (notification.status === "sent" && notification.sent_at) return `Sent ${relativeTime(notification.sent_at)}`;
  if (notification.status === "pending" && notification.next_attempt_at) {
    const nextAttempt = new Date(notification.next_attempt_at);
    if (!Number.isNaN(nextAttempt.getTime()) && nextAttempt.getTime() > Date.now()) {
      return `Sends ${shortDateFormatter.format(nextAttempt)}`;
    }
    return "Due now";
  }
  return relativeTime(notification.created_at);
}

function openEmailNotificationModal(notification) {
  const content = el("div", { className: "email-detail" }, [
    el("div", { className: "fact-list" }, [
      factBlock("Status", labelize(notification.status)),
      factBlock("Event", notification.event),
      factBlock("Recipient", notification.recipient_email),
      factBlock("Subject", notification.subject),
      factBlock("Created", relativeTime(notification.created_at)),
      factBlock("Attempts", notification.attempts || 0)
    ]),
    notification.last_error ? el("div", { className: "description" }, notification.last_error) : el("div"),
    el("h4", { className: "section-title" }, "Body"),
    el("pre", { className: "email-body" }, notification.body_text || "")
  ]);
  els.modalHost.open({
    title: "Email Notification",
    hideFooter: notification.status !== "failed",
    submitText: "Retry",
    content,
    onSubmit: notification.status === "failed" ? async () => {
      await retryEmailNotification(notification.id);
    } : null
  });
}

function renderAuditEvents() {
  if (els.auditSearchInput && els.auditSearchInput.value !== state.auditPage.q) {
    els.auditSearchInput.value = state.auditPage.q;
  }
  els.auditList.replaceChildren();
  if (state.auditEvents.length === 0) {
    els.auditList.append(emptyInline({
      title: "No audit events",
      body: "Security and admin actions will appear here."
    }));
    renderPager(els.auditPager, state.auditPage, (offset) => {
      state.auditPage.offset = offset;
      loadAuditEvents().catch(showError);
    });
    return;
  }
  for (const event of state.auditEvents) {
    const row = el("div", { className: "admin-row" });
    row.append(
      el("div", { className: "admin-row-main" }, [
        el("strong", {}, labelize(event.action)),
        el("span", {}, `${event.actor_email || "system"} / ${event.target_type}${event.target_name ? ` / ${event.target_name}` : ""}`)
      ]),
      el("span", { className: "muted" }, relativeTime(event.created_at))
    );
    els.auditList.append(row);
  }
  renderPager(els.auditPager, state.auditPage, (offset) => {
    state.auditPage.offset = offset;
    loadAuditEvents().catch(showError);
  });
}

function renderPager(container, page, onChange) {
  if (!container) return;
  const total = Number(page.total || 0);
  const limit = Number(page.limit || 25);
  const offset = Number(page.offset || 0);
  const start = total === 0 ? 0 : Math.min(offset + 1, total);
  const end = Math.min(offset + limit, total);
  const previous = el("button", { className: "ghost-button", type: "button" }, "Previous");
  const next = el("button", { className: "ghost-button", type: "button" }, "Next");
  previous.disabled = offset <= 0;
  next.disabled = offset + limit >= total;
  previous.addEventListener("click", () => onChange(Math.max(0, offset - limit)));
  next.addEventListener("click", () => onChange(offset + limit));
  container.replaceChildren(
    el("span", { className: "pager-summary" }, `${start}-${end} of ${total}`),
    previous,
    next
  );
}

async function updateUser(id, patch) {
  await request(`/api/users/${id}`, { method: "PATCH", body: JSON.stringify(patch) });
  await Promise.all([loadUsers(), app.loadProducts()]);
}

async function deleteUser(id) {
  await request(`/api/users/${id}`, { method: "DELETE" });
  await Promise.all([loadUsers(), app.loadProducts()]);
}

async function deleteToken(id) {
  await request(`/api/tokens/${id}`, { method: "DELETE" });
  await loadTokens();
}

function openUserModal(user = null) {
  const editing = Boolean(user);
  if (editing) {
    els.modalHost.open({
      title: `Edit ${accountName(user) || user.email}`,
      submitText: "Save",
      content: accountEditContent(user),
      onSubmit: async (data) => {
        const patch = {
          display_name: data.display_name || "",
          email: data.email || ""
        };
        if (user.id !== state.user.id) {
          patch.role = data.role;
          patch.disabled = Boolean(data.disabled);
        }
        await updateUser(user.id, patch);
      }
    });
    return;
  }

  const fields = [
    { group: [
      { name: "email", label: "Email", type: "email", required: true, autocomplete: "email" },
      { name: "display_name", label: "Display name", autocomplete: "off" }
    ] },
    { name: "role", label: "Role", type: "select", options: selectOptions(state.meta.roles), value: "staff" },
    { group: [
      { name: "password", label: "Manual password (optional)", type: "password", minlength: 8, autocomplete: "new-password" },
      { name: "password_confirm", label: "Confirm manual password", type: "password", minlength: 8, autocomplete: "new-password" }
    ] }
  ];

  els.modalHost.open({
    title: "New Account",
    submitText: "Create Account",
    values: { role: "staff" },
    fields,
    onSubmit: async (data) => {
      if (data.password || data.password_confirm) {
        if (data.password !== data.password_confirm) {
          throw new Error("Manual passwords do not match");
        }
      } else {
        delete data.password;
      }
      delete data.password_confirm;
      const created = await request("/api/users", { method: "POST", body: JSON.stringify(data) });
      await loadUsers();
      if (created.account_link) {
        window.setTimeout(() => openAccountLinkResult(created, "setup"), 0);
      } else {
        showAppAlert("Account created.");
      }
    }
  });
}

function accountEditContent(user) {
  const self = user.id === state.user.id;
  const displayName = el("input", {
    name: "display_name",
    autocomplete: "off",
    value: user.display_name || ""
  });
  const email = el("input", {
    name: "email",
    type: "email",
    autocomplete: "email",
    value: user.email || ""
  });
  const role = el("select", { name: "role" });
  for (const option of selectOptions(state.meta.roles)) {
    role.append(new Option(option.label, option.value));
  }
  role.value = user.role || "staff";
  role.disabled = self;

  const disabled = el("input", { name: "disabled", type: "checkbox" });
  disabled.checked = Boolean(user.disabled);
  disabled.disabled = self;

  const reset = el("button", {
    className: "ghost",
    type: "button",
    "data-account-action": "reset"
  }, "Send reset link");
  reset.disabled = self || user.disabled;
  const remove = el("button", {
    className: "danger",
    type: "button",
    "data-account-action": "delete"
  }, "Delete account");
  remove.disabled = self;
  const confirmArea = el("div", { className: "account-confirm-area" });
  const content = el("div", { className: "account-edit" }, [
    el("div", { className: "account-edit-grid" }, [
      formField("Display name", displayName),
      formField("Email", email),
      formField("Role", role),
      el("label", { className: "check" }, [disabled, "Disabled"])
    ]),
    el("section", { className: "account-manage" }, [
      el("div", { className: "account-manage-head" }, [
        el("strong", {}, "Account access"),
        el("span", {}, self ? "You cannot reset or delete your own account." : "Use these actions only when the account owner cannot sign in or no longer needs access.")
      ]),
      el("div", { className: "account-action-row" }, [reset, remove]),
      confirmArea
    ])
  ]);

  reset.addEventListener("click", () => showInlineConfirm(confirmArea, {
    title: "Send a password reset link?",
    body: `${accountName(user)} will receive a one-time link if email is configured. Existing sessions are not changed.`,
    confirmLabel: "Send reset link",
    onConfirm: () => resetUserPassword(user)
  }));
  remove.addEventListener("click", () => showInlineConfirm(confirmArea, {
    title: "Delete this account?",
    body: `This permanently removes ${accountName(user)}. This cannot be undone.`,
    confirmLabel: "Delete account",
    danger: true,
    onConfirm: async () => {
      await deleteUser(user.id);
      els.modalHost.close();
    }
  }));
  return content;
}

async function resetUserPassword(user) {
  const payload = await request(`/api/users/${user.id}/password-reset`, { method: "POST" });
  await loadUsers();
  openAccountLinkResult(payload, "reset");
}

function openAccountLinkResult(payload, purpose = "setup") {
  const link = payload.account_link || {};
  const userLabel = accountName(payload) || "Account";
  const title = purpose === "reset" ? `Password Reset for ${userLabel}` : `Setup Link for ${userLabel}`;
  const linkInput = el("input", {
    readonly: "readonly",
    value: link.url || ""
  });
  const copy = el("button", { type: "button" }, "Copy");
  copy.addEventListener("click", async () => {
    await copyText(link.url || "");
    copy.textContent = "Copied";
    window.setTimeout(() => {
      copy.textContent = "Copy";
    }, 1200);
  });
  const hasEmail = Boolean(payload.email);
  const statusText = !hasEmail
    ? "This account has no email address. Share this one-time link manually."
    : link.email_enabled
    ? link.email_queued
      ? "Pappice queued the email. Keep this link as a fallback for manual delivery."
      : "Email is configured, but the message could not be queued. Use the link below."
    : "Email is not configured. Share this one-time link manually.";
  const content = el("div", { className: "link-result" }, [
    el("p", {}, statusText),
    el("div", { className: "copy-row" }, [linkInput, copy]),
    el("div", { className: "link-meta" }, [
      el("span", {}, ["Account: ", el("strong", {}, userLabel)]),
      el("span", {}, ["Email: ", el("strong", {}, payload.email || "")]),
      link.expires_at ? el("span", {}, ["Expires: ", el("strong", {}, fullDateFormatter.format(new Date(link.expires_at)))]) : el("span")
    ])
  ]);
  els.modalHost.open({
    title,
    content,
    hideFooter: true
  });
}

function openTokenModal() {
  els.modalHost.open({
    title: "Create API Token",
    submitText: "Create",
    fields: [{ name: "name", label: "Name", placeholder: "CLI", autocomplete: "off" }],
    onSubmit: async (data) => {
      const payload = await request("/api/tokens", { method: "POST", body: JSON.stringify(data) });
      els.tokenResult.textContent = payload.value;
      await loadTokens();
    }
  });
}

function openTestEmailModal() {
  els.modalHost.open({
    title: "Send Test Email",
    submitText: "Queue Test",
    fields: [
      { name: "email", label: "Recipient", type: "email", value: state.user?.email || "", required: true, autocomplete: "email" }
    ],
    onSubmit: async (data) => {
      await request("/api/email-notifications/test", { method: "POST", body: JSON.stringify({ email: data.email }) });
      await loadEmailNotifications();
    }
  });
}

async function retryEmailNotification(id) {
  await request(`/api/email-notifications/${id}/retry`, { method: "POST" });
  await loadEmailNotifications();
}
