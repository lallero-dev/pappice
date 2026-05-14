export class PemmeceModal extends HTMLElement {
  constructor() {
    super();
    this.attachShadow({ mode: "open" });
    this.shadowRoot.innerHTML = `
      <link rel="stylesheet" href="/static/components.css">
      <dialog>
        <form method="dialog">
          <header>
            <h2></h2>
            <button class="icon" type="button" value="cancel" aria-label="Close">x</button>
          </header>
          <p class="error" role="alert" hidden></p>
          <section part="body"></section>
          <footer>
            <button class="ghost" type="button" value="cancel">Cancel</button>
            <button class="primary" type="submit">Save</button>
          </footer>
        </form>
      </dialog>
    `;
    this.dialog = this.shadowRoot.querySelector("dialog");
    this.form = this.shadowRoot.querySelector("form");
    this.titleNode = this.shadowRoot.querySelector("h2");
    this.errorNode = this.shadowRoot.querySelector(".error");
    this.bodyNode = this.shadowRoot.querySelector("section");
    this.footerNode = this.shadowRoot.querySelector("footer");
    this.submitButton = this.shadowRoot.querySelector(".primary");
    this.shadowRoot.querySelectorAll("[value='cancel']").forEach((button) => {
      button.addEventListener("click", () => this.close());
    });
  }

  open({ title, submitText = "Save", fields = [], values = {}, content = null, size = "", hideFooter = false, onSubmit }) {
    this.titleNode.textContent = title;
    this.dialog.classList.toggle("wide", size === "wide");
    this.footerNode.hidden = hideFooter;
    this.submitButton.textContent = submitText;
    this.submitButton.disabled = false;
    this.errorNode.hidden = true;
    this.errorNode.textContent = "";
    if (content) {
      this.bodyNode.replaceChildren(content);
    } else {
      this.bodyNode.replaceChildren(...fields.map((field) => modalField(field, values[field.name], values)));
    }
    this.form.onsubmit = async (event) => {
      event.preventDefault();
      if (!onSubmit) return;
      const data = Object.fromEntries(new FormData(this.form).entries());
      for (const checkbox of this.bodyNode.querySelectorAll("input[type='checkbox']")) {
        data[checkbox.name] = checkbox.checked;
      }
      this.submitButton.disabled = true;
      this.submitButton.setAttribute("aria-busy", "true");
      this.errorNode.hidden = true;
      this.errorNode.textContent = "";
      try {
        await onSubmit(data, this.form);
        this.close();
      } catch (error) {
        this.errorNode.textContent = error?.message || "Request failed";
        this.errorNode.hidden = false;
        this.dispatchEvent(new CustomEvent("pm-modal-error", { detail: error }));
      } finally {
        this.submitButton.disabled = false;
        this.submitButton.removeAttribute("aria-busy");
      }
    };
    if (!this.dialog.open) this.dialog.showModal();
    requestAnimationFrame(() => this.bodyNode.querySelector("input, select, textarea, button")?.focus());
  }

  close() {
    this.dialog.close();
  }

  isOpen() {
    return this.dialog.open;
  }
}

function modalField(field, value, values = {}) {
  if (field.group) {
    return el("div", { className: "grid" }, field.group.map((item) => modalField(item, values[item.name], values)));
  }
  if (field.type === "checkbox") {
    const input = el("input", { type: "checkbox", name: field.name });
    input.checked = value !== undefined ? Boolean(value) : Boolean(field.checked);
    return el("label", { className: "check" }, [input, field.label]);
  }
  const label = el("label", {}, [field.label || field.name]);
  let control;
  if (field.type === "select") {
    control = el("select", { name: field.name });
    for (const option of field.options || []) {
      control.append(new Option(option.label, option.value));
    }
  } else if (field.type === "textarea") {
    control = el("textarea", { name: field.name, rows: field.rows || 4 });
  } else {
    control = el("input", { name: field.name, type: field.type || "text" });
  }
  if (field.required) control.required = true;
  if (field.disabled) control.disabled = true;
  if (field.minlength) control.minLength = field.minlength;
  if (field.maxlength) control.maxLength = field.maxlength;
  if (field.min !== undefined) control.min = field.min;
  if (field.max !== undefined) control.max = field.max;
  if (field.step !== undefined) control.step = field.step;
  if (field.placeholder) control.placeholder = field.placeholder;
  if (field.autocomplete) control.autocomplete = field.autocomplete;
  if (value !== undefined) control.value = value;
  else if (field.value !== undefined) control.value = field.value;
  label.append(control);
  return label;
}

export function el(tag, props = {}, content) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key === "className") node.className = value;
    else if (key === "type") node.type = value;
    else node.setAttribute(key, value);
  }
  if (Array.isArray(content)) {
    for (const item of content) node.append(item instanceof Node ? item : document.createTextNode(String(item)));
  } else if (content instanceof Node) {
    node.append(content);
  } else if (content !== undefined) {
    node.textContent = content;
  }
  return node;
}

export function badge(value, className) {
  return el("span", { className: `badge ${className}` }, labelize(value));
}

export function fillOptions(select, values, emptyLabel) {
  select.replaceChildren();
  if (emptyLabel) select.append(new Option(emptyLabel, ""));
  for (const value of values) select.append(new Option(labelize(value), value));
}

export function fillSelect(select, options, emptyLabel) {
  select.replaceChildren();
  if (emptyLabel) select.append(new Option(emptyLabel, ""));
  for (const option of options) select.append(new Option(option.label, option.value));
}

export function labelize(value) {
  return String(value || "").replace(/_/g, " ").replace(/\b\w/g, (char) => char.toUpperCase());
}

export function relativeTime(value) {
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

export function debounce(fn, wait) {
  let timeout = 0;
  return (...args) => {
    window.clearTimeout(timeout);
    timeout = window.setTimeout(() => fn(...args), wait);
  };
}

export function formObject(form) {
  return Object.fromEntries(form.entries());
}

export function splitList(value) {
  return String(value || "").split(/[,\s]+/).map((item) => item.trim()).filter(Boolean);
}

export function defineComponents() {
  if (!customElements.get("pm-modal")) {
    customElements.define("pm-modal", PemmeceModal);
  }
}
