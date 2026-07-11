import { request } from "./api.js";
import { badge, el, formObject, labelize, relativeTime, splitList } from "./components.js";
import { DEFAULT_PRODUCT_SECTION, PRODUCT_SECTIONS, els, state } from "./state.js";
import { accountLabel, accountName, canManageProduct, currentProductDetail, isAdmin, manageableProducts } from "./access.js";
import { confirmAction, copyText, emptyInline, formField, selectOptions, showAppAlert, showError, showInlineConfirm } from "./ui.js";

let app = {};
let sectionLoadRequestID = 0;
const sectionLoaders = {
  general: () => renderProductGeneral(),
  members: (requestID) => Promise.all([app.loadUsers(), loadMembers(requestID)]),
  webhooks: loadProductWebhooks,
  deliveries: loadProductDeliveries
};

export function initProducts(options) {
  app = options;
  bindProductEvents();
}

function bindProductEvents() {
  els.productTab.addEventListener("click", () => openProductsIndex().catch(showError));
  for (const button of els.productSectionButtons) {
    button.addEventListener("click", () => switchProductSection(button.getAttribute("data-product-section")).catch(showError));
  }
  els.addProductButton.addEventListener("click", openProductModal);
  els.productGeneralForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (!canManageProduct(state.productDetailId)) return;
    els.saveProductButton.disabled = true;
    els.saveProductButton.setAttribute("aria-busy", "true");
    try {
      await updateCurrentProduct(formObject(new FormData(els.productGeneralForm)));
    } catch (error) {
      showError(error);
    } finally {
      els.saveProductButton.disabled = false;
      els.saveProductButton.removeAttribute("aria-busy");
    }
  });
  els.productIndexList.addEventListener("click", (event) => {
    const open = event.target.closest?.("[data-product-open]");
    if (open) openProductDetail(Number(open.getAttribute("data-product-open"))).catch(showError);
  });
  els.deleteProductButton.addEventListener("click", () => deleteCurrentProduct().catch(showError));
  els.addMemberButton.addEventListener("click", () => openMemberModal());
  els.addGlobalWebhookButton.addEventListener("click", () => openWebhookModal("global"));
  els.addWebhookButton.addEventListener("click", () => openWebhookModal("product"));
}

export async function loadProductView() {
  renderProductsView();
  if (state.productMode !== "detail" || !state.productDetailId || !canManageProduct(state.productDetailId)) return;
  await loadProductSection(state.productSection);
}

async function loadProductSection(section) {
  state.productSection = validProductSection(section) ? section : DEFAULT_PRODUCT_SECTION;
  renderProductSections();
  await sectionLoaders[state.productSection](++sectionLoadRequestID);
}

async function loadMembers(requestID = ++sectionLoadRequestID) {
  const productID = state.productDetailId;
  const payload = await request(`/api/products/${productID}/members`);
  if (requestID !== sectionLoadRequestID || state.productDetailId !== productID) return;
  state.members = payload.members || [];
  renderMembers();
}

async function loadProductWebhooks(requestID = ++sectionLoadRequestID) {
  const productID = state.productDetailId;
  const payload = await request(`/api/products/${productID}/webhooks`);
  if (requestID !== sectionLoadRequestID || state.productDetailId !== productID) return;
  state.webhooks = payload.webhooks || [];
  renderWebhooks(els.webhookList, state.webhooks);
}

export async function loadGlobalWebhooks() {
  const payload = await request("/api/webhooks");
  state.globalWebhooks = payload.webhooks || [];
  renderWebhooks(els.globalWebhookList, state.globalWebhooks);
}

async function loadProductDeliveries(requestID = ++sectionLoadRequestID) {
  const productID = state.productDetailId;
  const payload = await request(`/api/products/${productID}/webhook-deliveries`);
  if (requestID !== sectionLoadRequestID || state.productDetailId !== productID) return;
  state.deliveries = payload.deliveries || [];
  renderDeliveries();
}

function renderProductSections() {
  for (const button of els.productSectionButtons) {
    const active = button.getAttribute("data-product-section") === state.productSection;
    button.classList.toggle("active", active);
    button.setAttribute("aria-current", active ? "page" : "false");
  }
  for (const panel of els.productSectionPanels) {
    panel.hidden = panel.getAttribute("data-product-panel") !== state.productSection;
  }
}

function renderProductsView() {
  const showDetail = state.productMode === "detail" && state.productDetailId && canManageProduct(state.productDetailId);
  els.productIndexPanel.hidden = Boolean(showDetail);
  els.productDetailView.hidden = !showDetail;
  if (!showDetail) {
    state.productMode = "index";
    renderProductIndex();
    return;
  }
  renderProductContext();
  renderProductGeneral();
  renderProductSections();
}

async function switchProductSection(section) {
  state.productSection = validProductSection(section) ? section : DEFAULT_PRODUCT_SECTION;
  if (state.view === "product") app.syncRoute();
  if (state.productMode === "detail" && state.productDetailId && canManageProduct(state.productDetailId)) {
    await loadProductSection(state.productSection);
  }
}

function validProductSection(section) {
  return PRODUCT_SECTIONS.includes(section);
}

export function renderProductIndex() {
  els.addProductButton.hidden = !isAdmin();
  els.productIndexList.replaceChildren();
  const products = manageableProducts();
  if (products.length === 0) {
    els.productIndexList.append(emptyInline({
      title: "No products",
      body: isAdmin()
        ? "Create a product before inviting customers."
        : "Ask an admin to grant manager access before managing product settings.",
      actionLabel: isAdmin() ? "New Product" : "",
      onAction: isAdmin() ? openProductModal : null
    }));
    return;
  }
  for (const product of products) {
    const row = el("div", { className: "admin-row product-index-row" });
    const open = el("button", {
      className: "ghost-button",
      type: "button",
      "data-product-open": String(product.id)
    }, "Open");
    row.append(
      el("div", { className: "admin-row-main" }, [
        el("strong", {}, product.name || product.key || `Product ${product.id}`),
        el("span", {}, product.key || `#${product.id}`)
      ]),
      el("span", { className: "muted" }, `${labelize(product.role || "manager")} access`),
      open
    );
    els.productIndexList.append(row);
  }
}

function renderProductContext() {
  const product = currentProductDetail();
  if (!els.productContextTitle || !els.productContextMeta) return;
  els.productContextMeta.replaceChildren();

  if (!product) {
    els.productContextTitle.textContent = "No product selected";
    els.productContextMeta.append(el("span", { className: "muted" }, "Choose a product to manage members and integrations."));
    return;
  }

  els.productContextTitle.textContent = product.name || product.key || "Product";
  els.productContextMeta.append(
    el("span", { className: "product-key-pill" }, product.key || `#${product.id}`),
    el("span", { className: "muted" }, `${labelize(product.role || "manager")} access`)
  );
}

function renderProductGeneral() {
  const product = currentProductDetail();
  if (!els.productGeneralForm) return;
  const controls = [
    els.productGeneralName,
    els.productGeneralDescription,
    els.saveProductButton
  ].filter(Boolean);

  if (!product) {
    els.productGeneralForm.reset();
    for (const control of controls) control.disabled = true;
    if (els.productDangerZone) els.productDangerZone.hidden = true;
    return;
  }

  if (els.productGeneralKey) els.productGeneralKey.value = product.key || "";
  if (els.productGeneralKey) els.productGeneralKey.disabled = true;
  if (els.productGeneralName) els.productGeneralName.value = product.name || "";
  if (els.productGeneralDescription) els.productGeneralDescription.value = product.description || "";
  for (const control of controls) control.disabled = !canManageProduct(product.id);
  if (els.productDangerZone) els.productDangerZone.hidden = !isAdmin();
  if (els.deleteProductButton) els.deleteProductButton.dataset.productId = String(product.id);
}

function renderMembers() {
  els.memberList.replaceChildren();
  if (state.members.length === 0) {
    els.memberList.append(emptyInline({
      title: "No product members",
      body: "Add staff and customers who should access this product.",
      actionLabel: "Add Member",
      onAction: openMemberModal
    }));
    return;
  }
  for (const member of state.members) {
    const row = el("div", { className: "admin-row", "data-member-user": String(member.user_id) });
    const edit = el("button", { className: "ghost-button", type: "button" }, "Edit");
    edit.addEventListener("click", () => openMemberModal(member));
    row.append(
      el("div", { className: "admin-row-main" }, accountLabel(member)),
      badge(member.role, "priority-normal"),
      edit
    );
    els.memberList.append(row);
  }
}

function renderWebhooks(list, hooks) {
  list.replaceChildren();
  if (hooks.length === 0) {
    const global = list === els.globalWebhookList;
    list.append(emptyInline({
      title: "No webhooks",
      body: global ? "Global webhooks receive events from every product." : "Product webhooks only receive events for this product.",
      actionLabel: "New Webhook",
      onAction: () => openWebhookModal(global ? "global" : "product")
    }));
    return;
  }
  for (const hook of hooks) {
    const row = el("div", { className: "admin-row" });
    const edit = el("button", { className: "ghost-button", type: "button" }, "Edit");
    edit.addEventListener("click", () => openWebhookEditModal(hook, list === els.globalWebhookList ? "global" : "product"));
    const test = el("button", { className: "ghost-button", type: "button" }, "Test");
    test.addEventListener("click", () => testWebhook(hook.id).catch(showError));
    const remove = el("button", { className: "ghost-button", type: "button" }, "Delete");
    remove.addEventListener("click", () => deleteWebhook(hook.id).catch(showError));
    row.append(
      el("div", { className: "admin-row-main" }, `${hook.name} / ${hook.url}`),
      el("span", { className: hook.enabled ? "status-ok" : "muted" }, hook.enabled ? "Enabled" : "Disabled"),
      webhookLastStatus(hook),
      edit,
      test,
      remove
    );
    list.append(row);
  }
}

function webhookLastStatus(hook) {
  if (hook.last_error) {
    return el("span", { className: "status-error", title: hook.last_error }, "Failed");
  }
  if (hook.last_status) {
    const ok = Number(hook.last_status) >= 200 && Number(hook.last_status) <= 299;
    return el("span", { className: ok ? "status-ok" : "status-error" }, `HTTP ${hook.last_status}`);
  }
  return el("span", { className: "muted" }, "No deliveries");
}

function renderDeliveries() {
  els.deliveryList.replaceChildren();
  if (state.deliveries.length === 0) {
    els.deliveryList.append(emptyInline({
      title: "No deliveries",
      body: "Webhook delivery attempts will appear here after events are sent."
    }));
    return;
  }
  for (const delivery of state.deliveries) {
    const row = el("div", { className: "admin-row" });
    row.append(
      el("div", { className: "admin-row-main" }, `${delivery.event} / ${delivery.error || `HTTP ${delivery.status_code || 0}`}`),
      el("span", { className: "muted" }, `${delivery.duration_ms}ms`),
      el("span", { className: "muted" }, relativeTime(delivery.created_at))
    );
    els.deliveryList.append(row);
  }
}

async function upsertMember(input) {
  await request(`/api/products/${state.productDetailId}/members`, { method: "POST", body: JSON.stringify(input) });
  await app.loadProducts();
  if (state.productDetailId && canManageProduct(state.productDetailId)) await loadMembers();
}

async function deleteMember(userId) {
  await request(`/api/products/${state.productDetailId}/members/${userId}`, { method: "DELETE" });
  await app.loadProducts();
  if (state.productDetailId && canManageProduct(state.productDetailId)) await loadMembers();
}

async function deleteWebhook(id) {
  await request(`/api/webhooks/${id}`, { method: "DELETE" });
  await refreshWebhookLists();
}

async function refreshWebhookLists() {
  if (isAdmin()) await loadGlobalWebhooks();
  if (state.view === "product" && state.productDetailId && canManageProduct(state.productDetailId)) {
    await loadProductWebhooks();
  }
}

async function testWebhook(id) {
  const delivery = await request(`/api/webhooks/${id}/test`, { method: "POST" });
  await refreshWebhookLists();
  if (state.view === "product" && state.productDetailId && canManageProduct(state.productDetailId)) {
    await loadProductDeliveries();
  }
  openWebhookTestResult(delivery);
}

function openWebhookTestResult(delivery) {
  const ok = webhookDeliverySucceeded(delivery);
  const status = delivery.status_code ? `HTTP ${delivery.status_code}` : "No HTTP response";
  const message = ok
    ? `Receiver accepted the test delivery with ${status}.`
    : delivery.error || `Receiver rejected the test delivery with ${status}.`;
  const content = el("div", { className: "link-result" }, [
    el("p", {}, message),
    el("div", { className: "link-meta" }, [
      el("span", {}, ["Event: ", el("strong", {}, delivery.event || "webhook.test")]),
      el("span", {}, ["Status: ", el("strong", {}, status)]),
      el("span", {}, ["Duration: ", el("strong", {}, `${delivery.duration_ms || 0}ms`)])
    ])
  ]);
  els.modalHost.open({
    title: ok ? "Webhook Test Succeeded" : "Webhook Test Failed",
    content,
    hideFooter: true
  });
}

function webhookDeliverySucceeded(delivery) {
  const status = Number(delivery?.status_code || 0);
  return !delivery?.error && status >= 200 && status <= 299;
}

export function openProductModal() {
  els.modalHost.open({
    title: "New Product",
    submitText: "Create",
    fields: [
      { group: [
        { name: "key", label: "Key", required: true, maxlength: 16, placeholder: "OPS", autocomplete: "off" },
        { name: "name", label: "Name", required: true, placeholder: "Operations", autocomplete: "off" }
      ] },
      { name: "description", label: "Description", type: "textarea", rows: 3 }
    ],
    onSubmit: async (data) => {
      const product = await request("/api/products", { method: "POST", body: JSON.stringify(data) });
      await app.loadProducts();
      await openProductDetail(product.id);
    }
  });
}

async function updateCurrentProduct(data) {
  const product = currentProductDetail();
  if (!product || !canManageProduct(product.id)) return;
  const patch = {
    name: String(data.name || "").trim(),
    description: String(data.description || "").trim()
  };
  await request(`/api/products/${product.id}`, { method: "PATCH", body: JSON.stringify(patch) });
  await app.loadProducts();
  renderProductsView();
  showAppAlert("Product updated.");
}

function openMemberModal(member = null) {
  if (member) {
    els.modalHost.open({
      title: `Edit ${accountName(member)}`,
      submitText: "Save",
      content: memberEditContent(member),
      onSubmit: async (data) => {
        await upsertMember({ user_id: member.user_id, role: data.role });
      }
    });
    return;
  }
  const users = state.users
    .filter((user) => !user.disabled)
    .map((user) => ({ value: String(user.id), label: accountLabel(user) }));
  els.modalHost.open({
    title: "Add Product Member",
    submitText: "Save",
    fields: [
      { name: "user_id", label: "Account", type: "select", options: users },
      { name: "role", label: "Role", type: "select", options: selectOptions(state.meta.productRoles), value: "viewer" }
    ],
    onSubmit: async (data) => {
      await upsertMember({ user_id: Number(data.user_id), role: data.role });
    }
  });
}

function memberEditContent(member) {
  const role = el("select", { name: "role" });
  for (const option of selectOptions(state.meta.productRoles)) {
    role.append(new Option(option.label, option.value));
  }
  role.value = member.role || "viewer";
  const remove = el("button", {
    className: "danger",
    type: "button",
    "data-member-action": "delete"
  }, "Remove member");
  const confirmArea = el("div", { className: "account-confirm-area" });
  const content = el("div", { className: "account-edit" }, [
    el("div", { className: "account-edit-grid" }, [
      formField("Account", el("input", {
        disabled: "disabled",
        value: accountLabel(member)
      })),
      formField("Role", role)
    ]),
    el("section", { className: "account-manage" }, [
      el("div", { className: "account-manage-head" }, [
        el("strong", {}, "Membership"),
        el("span", {}, "Remove this account only when it should no longer access this product.")
      ]),
      el("div", { className: "account-action-row" }, [remove]),
      confirmArea
    ])
  ]);

  remove.addEventListener("click", () => showInlineConfirm(confirmArea, {
    title: "Remove this member?",
    body: `${accountName(member)} will lose access to this product. Existing tickets and comments are kept.`,
    confirmLabel: "Remove member",
    danger: true,
    onConfirm: async () => {
      await deleteMember(member.user_id);
      els.modalHost.close();
    }
  }));
  return content;
}

function openWebhookModal(scope) {
  els.modalHost.open({
    title: scope === "global" ? "New Global Webhook" : "New Product Webhook",
    submitText: "Create",
    fields: [
      { group: [
        { name: "name", label: "Name", required: true, autocomplete: "off" },
        { name: "url", label: "URL", required: true, placeholder: "https://example.test/hook", autocomplete: "off" }
      ] },
      { name: "secret", label: "Signing secret", placeholder: "Leave empty to generate one", autocomplete: "off" },
      { name: "events", label: "Events", placeholder: "ticket.created, ticket.updated, ticket.commented" },
      { name: "enabled", label: "Enabled", type: "checkbox", checked: true }
    ],
    onSubmit: async (data) => {
      const payload = { ...data, events: splitList(data.events), enabled: Boolean(data.enabled) };
      if (!String(payload.secret || "").trim()) delete payload.secret;
      let result;
      if (scope === "global") {
        result = await request("/api/webhooks", { method: "POST", body: JSON.stringify(payload) });
      } else {
        result = await request(`/api/products/${state.productDetailId}/webhooks`, { method: "POST", body: JSON.stringify(payload) });
      }
      await refreshWebhookLists();
      openWebhookSecretResult(result.webhook, result.secret, "created");
      return false;
    }
  });
}

function openWebhookEditModal(hook, scope) {
  const name = el("input", { name: "name", autocomplete: "off", required: "required", value: hook.name || "" });
  const url = el("input", { name: "url", autocomplete: "off", required: "required", value: hook.url || "" });
  const events = el("input", {
    name: "events",
    autocomplete: "off",
    placeholder: "ticket.created, ticket.updated, ticket.commented",
    value: (hook.events || []).join(", ")
  });
  const enabled = el("input", { name: "enabled", type: "checkbox" });
  enabled.checked = Boolean(hook.enabled);
  const rotate = el("button", { className: "ghost-button", type: "button" }, "Rotate Secret");
  const confirmArea = el("div", { className: "account-confirm-area" });
  const content = el("div", { className: "account-edit" }, [
    el("div", { className: "account-edit-grid" }, [
      formField("Name", name),
      formField("URL", url),
      formField("Events", events),
      el("label", { className: "check" }, [enabled, "Enabled"])
    ]),
    el("section", { className: "account-manage" }, [
      el("div", { className: "account-manage-head" }, [
        el("strong", {}, "Signing Secret"),
        el("span", {}, hook.has_secret
          ? "Stored secrets are not shown again. Rotate when the receiver needs a new verification secret."
          : "This webhook has no signing secret.")
      ]),
      el("div", { className: "account-action-row" }, [rotate]),
      confirmArea
    ])
  ]);
  rotate.addEventListener("click", () => showInlineConfirm(confirmArea, {
    title: "Rotate webhook secret?",
    body: "Pappice will immediately sign future deliveries with the new secret. Update the receiver before relying on this webhook.",
    confirmLabel: "Rotate Secret",
    onConfirm: async () => {
      const result = await request(`/api/webhooks/${hook.id}/secret`, { method: "POST" });
      await refreshWebhookLists();
      openWebhookSecretResult(result.webhook, result.secret, "rotated");
    }
  }));
  els.modalHost.open({
    title: scope === "global" ? "Edit Global Webhook" : "Edit Product Webhook",
    submitText: "Save",
    content,
    onSubmit: async (data) => {
      await request(`/api/webhooks/${hook.id}`, {
        method: "PATCH",
        body: JSON.stringify({
          name: data.name,
          url: data.url,
          events: splitList(data.events),
          enabled: Boolean(data.enabled)
        })
      });
      await refreshWebhookLists();
    }
  });
}

function openWebhookSecretResult(hook, secret, action) {
  const secretInput = el("input", {
    readonly: "readonly",
    value: secret || ""
  });
  const copy = el("button", { type: "button" }, "Copy");
  copy.addEventListener("click", async () => {
    await copyText(secret || "");
    copy.textContent = "Copied";
    window.setTimeout(() => {
      copy.textContent = "Copy";
    }, 1200);
  });
  const content = el("div", { className: "link-result" }, [
    el("p", {}, `Webhook secret ${action}. Store it now; Pappice will not show this value again.`),
    el("div", { className: "copy-row" }, [secretInput, copy]),
    el("div", { className: "link-meta" }, [
      el("span", {}, ["Webhook: ", el("strong", {}, hook?.name || "")]),
      el("span", {}, ["Signature header: ", el("strong", {}, "X-Pappice-Signature")]),
      el("span", {}, ["Format: ", el("strong", {}, "sha256=<hmac>")])
    ])
  ]);
  els.modalHost.open({
    title: action === "rotated" ? "Webhook Secret Rotated" : "Webhook Secret Created",
    content,
    hideFooter: true
  });
}

async function openProductsIndex() {
  state.productMode = "index";
  state.productDetailId = null;
  app.switchView("product", { load: false });
  await loadProductView();
}

async function deleteCurrentProduct() {
  const product = currentProductDetail();
  if (!product || !isAdmin()) return;
  const confirmed = await confirmAction({
    title: "Delete this product?",
    body: "This permanently removes the product, its tickets, members, webhooks, email history, and delivery history.",
    confirmLabel: "Delete Product",
    danger: true,
    details: [
      ["Product", `${product.key || `#${product.id}`} / ${product.name || product.key || "Product"}`],
      ["Tickets", "All tickets in this product will be deleted"]
    ]
  });
  if (!confirmed) return;

  if (els.deleteProductButton) els.deleteProductButton.disabled = true;
  try {
    await request(`/api/products/${product.id}`, { method: "DELETE" });
    if (state.ticketProductId === product.id) state.ticketProductId = null;
    app.setSelectedTicket(null, { updateRoute: false });
    state.productMode = "index";
    state.productDetailId = null;
    state.productSection = DEFAULT_PRODUCT_SECTION;
    await app.loadProducts();
    renderProductsView();
    app.syncRoute({ replace: true });
    showAppAlert("Product deleted.");
  } finally {
    if (els.deleteProductButton) els.deleteProductButton.disabled = false;
  }
}

async function openProductDetail(productId, section = DEFAULT_PRODUCT_SECTION) {
  if (!canManageProduct(productId)) return;
  state.productMode = "detail";
  state.productDetailId = productId;
  state.productSection = validProductSection(section) ? section : DEFAULT_PRODUCT_SECTION;
  app.switchView("product", { load: false });
  await loadProductView();
}
