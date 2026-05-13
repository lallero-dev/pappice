class SupportPortal extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({ mode: "open" });
    this.state = {
      projects: [],
      ticket: null,
      user: null,
      csrf: "",
      error: "",
      success: "",
      loading: true,
      needsSetup: false
    };
  }

  connectedCallback() {
    this.render();
    this.loadSession();
  }

  async loadSession() {
    this.state.loading = true;
    this.state.error = "";
    this.render();
    try {
      const session = await request("/api/session");
      this.state.needsSetup = Boolean(session.needs_setup);
      this.state.user = session.authenticated ? session.user : null;
      this.state.csrf = session.csrf_token || "";
      if (this.state.user) {
        await this.loadSupportData();
      }
    } catch (error) {
      this.state.error = error.message || "Request failed";
    }
    this.state.loading = false;
    this.render();
  }

  async loadSupportData() {
    const token = ticketToken();
    if (token) {
      const payload = await this.api(`/api/support/tickets/${encodeURIComponent(token)}`);
      this.state.ticket = payload.ticket;
      this.state.projects = [];
      return;
    }
    const payload = await this.api("/api/support/projects");
    this.state.projects = payload.projects || [];
    this.state.ticket = null;
  }

  render() {
    this.shadowRoot.innerHTML = `
      <link rel="stylesheet" href="/static/support.css">
      <div class="page">
        <header class="topbar">
          <div class="brand">
            <span class="mark">P</span>
            <div>
              <strong>Pemmece</strong>
              <span>support portal</span>
            </div>
          </div>
          ${this.userActions()}
        </header>
        <main class="shell">
          ${this.state.error ? `<div class="error">${escapeHTML(this.state.error)}</div>` : ""}
          ${this.state.success ? `<div class="success">${escapeHTML(this.state.success)}</div>` : ""}
          ${this.mainView()}
        </main>
      </div>
    `;
    this.bind();
  }

  mainView() {
    if (this.state.loading) {
      return `<section class="panel"><p class="muted">Loading</p></section>`;
    }
    if (this.state.needsSetup) {
      return `
        <section class="panel">
          <h1>Setup Required</h1>
          <p class="muted">An administrator needs to finish Pemmece setup first.</p>
        </section>
      `;
    }
    if (!this.state.user) {
      return this.loginView();
    }
    return this.state.ticket ? this.ticketView() : this.createView();
  }

  userActions() {
    if (!this.state.user) return "";
    const name = this.state.user.display_name || this.state.user.username;
    return `
      <div class="user-actions">
        <span>${escapeHTML(name)}</span>
        <button id="logoutButton" type="button">Logout</button>
      </div>
    `;
  }

  loginView() {
    return `
      <section class="panel auth-panel">
        <h1>Sign In</h1>
        <form id="loginForm">
          <label>
            Username
            <input name="username" required autocomplete="username">
          </label>
          <label>
            Password
            <input name="password" required type="password" autocomplete="current-password">
          </label>
          <button type="submit">Sign In</button>
        </form>
      </section>
    `;
  }

  createView() {
    const options = this.state.projects.map((project) => (
      `<option value="${project.id}">${escapeHTML(project.key)} / ${escapeHTML(project.name)}</option>`
    )).join("");
    const disabled = this.state.projects.length === 0 ? "disabled" : "";
    return `
      <section class="panel">
        <h1>Open a Support Ticket</h1>
        <p class="muted">Your registered account email will receive no-reply ticket notifications.</p>
        <form id="ticketForm">
          <label>
            Project
            <select name="project_id" required ${disabled}>
              ${options || '<option value="">No support projects assigned</option>'}
            </select>
          </label>
          <label>
            Subject
            <input name="title" required maxlength="160">
          </label>
          <label>
            Details
            <textarea name="description" rows="7" required></textarea>
          </label>
          <button type="submit" ${disabled}>Send Request</button>
        </form>
      </section>
    `;
  }

  ticketView() {
    const ticket = this.state.ticket;
    const comments = (ticket.comments || []).map((comment) => `
      <article class="comment">
        <small>${escapeHTML(comment.author)} / ${relativeTime(comment.created_at)}</small>
        <div class="comment-body">${escapeHTML(comment.body)}</div>
      </article>
    `).join("");
    return `
      <section class="panel">
        <div class="ticket-head">
          <div>
            <h1>${escapeHTML(ticket.title)}</h1>
            <p class="muted">${escapeHTML(ticket.key)} / ${escapeHTML(ticket.project_key || "")}</p>
          </div>
          <span class="badge">${escapeHTML(ticket.status)}</span>
        </div>
        <div class="description">${escapeHTML(ticket.description || "No details provided.")}</div>
      </section>
      <section class="panel">
        <h2>Conversation</h2>
        <div class="comments">${comments || '<p class="muted">No replies yet.</p>'}</div>
        <form id="commentForm">
          <label>
            Reply
            <textarea name="body" rows="5" required></textarea>
          </label>
          <button type="submit">Send Reply</button>
        </form>
      </section>
    `;
  }

  bind() {
    const loginForm = this.shadowRoot.querySelector("#loginForm");
    if (loginForm) {
      loginForm.addEventListener("submit", (event) => this.login(event));
    }
    const logoutButton = this.shadowRoot.querySelector("#logoutButton");
    if (logoutButton) {
      logoutButton.addEventListener("click", () => this.logout());
    }
    const ticketForm = this.shadowRoot.querySelector("#ticketForm");
    if (ticketForm) {
      ticketForm.addEventListener("submit", (event) => this.createTicket(event));
    }
    const commentForm = this.shadowRoot.querySelector("#commentForm");
    if (commentForm) {
      commentForm.addEventListener("submit", (event) => this.addComment(event));
    }
  }

  async login(event) {
    event.preventDefault();
    const form = event.currentTarget;
    const button = form.querySelector("button");
    button.disabled = true;
    try {
      const payload = await request("/api/login", { method: "POST", body: JSON.stringify(formObject(new FormData(form))) });
      this.state.user = payload.user;
      this.state.csrf = payload.csrf_token || "";
      this.state.success = "";
      this.state.error = "";
      await this.loadSupportData();
    } catch (error) {
      this.state.error = error.message || "Request failed";
    }
    this.render();
  }

  async logout() {
    try {
      await this.api("/api/logout", { method: "POST" });
    } catch (_error) {
    }
    this.state.user = null;
    this.state.csrf = "";
    this.state.projects = [];
    this.state.ticket = null;
    this.state.success = "";
    this.state.error = "";
    this.render();
  }

  async createTicket(event) {
    event.preventDefault();
    const form = event.currentTarget;
    const button = form.querySelector("button");
    button.disabled = true;
    try {
      const data = formObject(new FormData(form));
      data.project_id = Number(data.project_id);
      const payload = await this.api("/api/support/tickets", { method: "POST", body: JSON.stringify(data) });
      window.history.replaceState(null, "", supportPath(payload.url));
      this.state.ticket = payload.ticket;
      this.state.success = "Support request sent.";
      this.state.error = "";
    } catch (error) {
      this.state.error = error.message || "Request failed";
    }
    this.render();
  }

  async addComment(event) {
    event.preventDefault();
    const form = event.currentTarget;
    const button = form.querySelector("button");
    button.disabled = true;
    try {
      const token = ticketToken();
      const data = formObject(new FormData(form));
      const payload = await this.api(`/api/support/tickets/${encodeURIComponent(token)}/comments`, { method: "POST", body: JSON.stringify(data) });
      this.state.ticket = payload.ticket;
      this.state.success = "Reply sent.";
      this.state.error = "";
    } catch (error) {
      this.state.error = error.message || "Request failed";
    }
    this.render();
  }

  api(path, options = {}) {
    return request(path, options, this.state.csrf);
  }
}

customElements.define("support-portal", SupportPortal);

async function request(path, options = {}, csrf = "") {
  const method = (options.method || "GET").toUpperCase();
  const headers = { "Content-Type": "application/json", ...(options.headers || {}) };
  if (!["GET", "HEAD", "OPTIONS"].includes(method) && csrf) {
    headers["X-Pemmece-CSRF"] = csrf;
  }
  const response = await fetch(path, {
    credentials: "same-origin",
    ...options,
    headers
  });
  const text = await response.text();
  const payload = text ? JSON.parse(text) : {};
  if (!response.ok) throw new Error(payload.error || "Request failed");
  return payload;
}

function ticketToken() {
  const match = window.location.pathname.match(/^\/support\/tickets\/([^/]+)$/);
  return match ? decodeURIComponent(match[1]) : "";
}

function supportPath(value) {
  try {
    const parsed = new URL(value || "", window.location.origin);
    if (parsed.pathname.startsWith("/support/tickets/")) {
      return `${parsed.pathname}${parsed.search}${parsed.hash}`;
    }
  } catch (_error) {
  }
  return "/support";
}

function formObject(form) {
  return Object.fromEntries(form.entries());
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;"
  }[char]));
}

function relativeTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const seconds = Math.round((Date.now() - date.getTime()) / 1000);
  if (seconds < 60) return "now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days}d`;
  return date.toLocaleDateString();
}
