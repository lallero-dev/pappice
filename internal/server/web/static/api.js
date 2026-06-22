import { state } from "./state.js";

export async function request(path, options = {}) {
  return requestWithCSRFRefresh(path, options, true);
}

async function requestWithCSRFRefresh(path, options, retryCSRF) {
  const method = String(options.method || "GET").toUpperCase();
  const bodyIsFormData = options.body instanceof FormData;
  const headers = { ...(options.headers || {}) };
  if (!bodyIsFormData && !headers["Content-Type"]) {
    headers["Content-Type"] = "application/json";
  }
  if (state.csrf && ["POST", "PATCH", "PUT", "DELETE"].includes(method)) {
    headers["X-Pappice-CSRF"] = state.csrf;
  }
  const response = await fetch(path, {
    credentials: "same-origin",
    headers,
    ...options
  });
  const text = await response.text();
  let payload = {};
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = { error: text };
    }
  }
  if (!response.ok) {
    if (retryCSRF && isUnsafeMethod(method) && response.status === 403 && isCSRFTokenError(payload)) {
      const refreshed = await refreshCSRFToken();
      if (refreshed) return requestWithCSRFRefresh(path, options, false);
    }
    const error = new Error(payload.error || response.statusText || "Request failed");
    error.status = response.status;
    error.payload = payload;
    throw error;
  }
  return payload;
}

function isUnsafeMethod(method) {
  return ["POST", "PATCH", "PUT", "DELETE"].includes(method);
}

function isCSRFTokenError(payload) {
  return String(payload?.error || "").toLowerCase().includes("csrf token");
}

async function refreshCSRFToken() {
  try {
    const response = await fetch("/api/session", {
      credentials: "same-origin",
      headers: { Accept: "application/json" }
    });
    if (!response.ok) return false;
    const session = await response.json();
    if (!session.authenticated || !session.csrf_token) return false;
    state.csrf = session.csrf_token;
    state.user = session.user || state.user;
    return true;
  } catch {
    return false;
  }
}
