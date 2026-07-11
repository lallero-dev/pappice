import { el, labelize } from "./components.js";
import { els } from "./state.js";

let alertTimer = 0;

export function emptyState(options) {
  return emptyMessage("empty-state", options);
}

export function emptyInline(options) {
  return emptyMessage("empty-inline", options);
}

function emptyMessage(className, { title, body, actionLabel = "", onAction = null }) {
  const node = el("div", { className }, [el("h3", {}, title), el("p", {}, body)]);
  if (actionLabel && onAction) {
    const button = el("button", { className: "ghost-button", type: "button" }, actionLabel);
    button.addEventListener("click", onAction);
    node.append(el("div", { className: "empty-actions" }, [button]));
  }
  return node;
}

export function sideSection(title, content) {
  return el("section", { className: "side-section" }, [
    el("h4", { className: "section-title" }, title),
    content
  ]);
}

export function factBlock(label, value) {
  return el("div", { className: "fact-block" }, [
    el("span", { className: "fact-label" }, label),
    el("strong", {}, value)
  ]);
}

export function formField(label, control) {
  return el("label", {}, [label, control]);
}

export function selectOptions(values) {
  return values.map((value) => ({ value, label: labelize(value) }));
}

export function confirmAction({ title, body, confirmLabel, details = [], danger = false, stacked = false }) {
  const host = stacked ? document.createElement("pappice-modal") : els.modalHost;
  if (!host) return Promise.resolve(window.confirm(body));
  if (stacked) document.body.append(host);
  return new Promise((resolve) => {
    let settled = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      resolve(value);
    };
    const detailList = details.length > 0
      ? el("dl", { className: "confirm-detail-list" }, details.flatMap(([label, value]) => [
        el("dt", {}, label),
        el("dd", {}, value)
      ]))
      : el("div");
    host.open({
      title,
      submitText: confirmLabel,
      submitClass: danger ? "danger" : "primary",
      content: el("div", { className: "send-confirm" }, [el("p", {}, body), detailList]),
      onSubmit: async () => finish(true)
    });
    host.shadowRoot?.querySelector("dialog")?.addEventListener("close", () => {
      finish(false);
      if (stacked) host.remove();
    }, { once: true });
  });
}

export function showInlineConfirm(container, { title, body, confirmLabel, danger = false, onConfirm }) {
  const confirm = el("button", { className: danger ? "danger" : "primary", type: "button" }, confirmLabel);
  const cancel = el("button", { className: "ghost", type: "button" }, "Cancel");
  cancel.addEventListener("click", () => container.replaceChildren());
  confirm.addEventListener("click", async () => {
    confirm.disabled = true;
    confirm.setAttribute("aria-busy", "true");
    try {
      await onConfirm();
    } catch (error) {
      showError(error);
      confirm.disabled = false;
      confirm.removeAttribute("aria-busy");
    }
  });
  container.replaceChildren(el("div", { className: danger ? "account-confirm danger-zone" : "account-confirm" }, [
    el("strong", {}, title),
    el("p", {}, body),
    el("div", { className: "account-confirm-actions" }, [cancel, confirm])
  ]));
}

export async function copyText(value) {
  if (!value) return;
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const area = document.createElement("textarea");
  area.value = value;
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.append(area);
  area.select();
  document.execCommand("copy");
  area.remove();
}

export function showAppAlert(message) {
  window.clearTimeout(alertTimer);
  els.appAlertText.textContent = message;
  els.appAlert.hidden = false;
  alertTimer = window.setTimeout(clearAppAlert, 8000);
}

export function clearAppAlert() {
  window.clearTimeout(alertTimer);
  alertTimer = 0;
  els.appAlert.hidden = true;
  els.appAlertText.textContent = "";
}

export function userMessage(error) {
  if (typeof error === "string") return error;
  const raw = String(error?.message || "Request failed").trim();
  if (!raw || raw === "Failed to fetch") return "Pappice could not be reached. Check the connection and try again.";
  if (raw.startsWith("validation failed: ")) return raw.replace("validation failed: ", "");
  if (error?.status === 403) return raw || "You do not have permission to do that.";
  if (error?.status === 404) return raw && raw !== "not found" ? raw : "The requested item was not found. Refresh the page and try again.";
  if (error?.status === 409) return raw || "This action conflicts with the current state.";
  if (error?.status === 429) return raw || "Too many attempts. Try again later.";
  if (error?.status >= 500) return "Pappice hit an internal error. Try again, then check the server logs if it persists.";
  return raw;
}

export function showError(error) {
  console.error(error);
  if (error?.status === 401) {
    document.dispatchEvent(new CustomEvent("pappice-auth-required"));
    return;
  }
  showAppAlert(userMessage(error));
}
