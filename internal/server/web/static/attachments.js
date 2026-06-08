import { el } from "./components.js";
import { state } from "./state.js";

export function ticketAttachmentField(label, name) {
  const control = document.createElement("input");
  control.type = "file";
  control.name = name;
  control.multiple = true;
  control.className = "attachment-input";
  control.dataset.ticketControl = "true";
  const allowed = state.meta.uploads?.allowed_types || [];
  if (allowed.length > 0 && !allowed.includes("*") && !allowed.includes("*/*")) {
    control.accept = allowed.join(",");
  }
  const preview = el("div", { className: "attachment-preview empty" });
  const trigger = el("button", { className: "attachment-trigger", type: "button" }, [
    attachmentIcon()
  ]);
  trigger.setAttribute("aria-label", "Attach files");
  trigger.title = "Attach files";
  const picker = el("div", { className: "attachment-picker" }, [
    control,
    trigger,
    preview
  ]);
  let filesBeforeBrowse = null;
  let syncingFiles = false;
  trigger.addEventListener("click", () => {
    filesBeforeBrowse = Array.from(control.files || []);
    control.click();
  });
  control.addEventListener("change", () => {
    if (syncingFiles) {
      renderAttachmentPreview(control, preview);
      return;
    }
    if (filesBeforeBrowse) {
      const previous = filesBeforeBrowse;
      filesBeforeBrowse = null;
      if (previous.length > 0) {
        syncingFiles = true;
        setAttachmentFiles(control, mergeAttachmentFiles(previous, Array.from(control.files || [])));
        syncingFiles = false;
        return;
      }
    }
    renderAttachmentPreview(control, preview);
  });
  bindAttachmentDropZone(picker, control, "dragging");
  return el("div", { className: "ticket-form-field attachment-field" }, [
    el("span", {}, label),
    picker
  ]);
}

function attachmentIcon() {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "attachment-trigger-icon");
  svg.setAttribute("width", "100%");
  svg.setAttribute("height", "100%");
  svg.setAttribute("viewBox", "-8 0 32 32");
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("focusable", "false");
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("d", "m 228,157 v 20 c 0,3.313 -2.687,6 -6,6 -3.313,0 -6,-2.687 -6,-6 v -18 c 0,-2.209 1.791,-4 4,-4 2.209,0 4,1.791 4,4 v 18 c 0,1.104 -0.896,2 -2,2 -1.104,0 -2,-0.896 -2,-2 v -16 h -2 v 16 c 0,2.209 1.791,4 4,4 2.209,0 4,-1.791 4,-4 v -18 c 0,-3.313 -2.687,-6 -6,-6 -3.313,0 -6,2.687 -6,6 v 19 c 0.493,3.945 3.921,7 8,7 4.079,0 7.507,-3.055 8,-7 v -21 h -2");
  path.setAttribute("transform", "matrix(0.77810864 0.82467535 -0.81977676 0.78275824 -26.45409 -298.86851)");
  svg.append(path);
  return svg;
}

function renderAttachmentPreview(input, preview) {
  cleanupAttachmentPreview(preview);
  const files = Array.from(input.files || []);
  preview.classList.toggle("empty", files.length === 0);
  preview.replaceChildren();
  if (files.length === 0) return;
  const urls = [];
  files.forEach((file, index) => {
    const filename = file.name || "Attachment";
    const url = URL.createObjectURL(file);
    urls.push(url);
    const imagePreview = isStagedImageFile(file);
    const remove = el("button", { className: "attachment-remove", type: "button", "aria-label": `Remove ${file.name}` }, "x");
    remove.addEventListener("click", () => {
      const next = Array.from(input.files || []).filter((_, fileIndex) => fileIndex !== index);
      setAttachmentFiles(input, next);
    });
    const linkContent = [
      el("span", { className: "attachment-chip-label" }, filename)
    ];
    if (imagePreview) {
      linkContent.unshift(el("img", { className: "attachment-chip-thumb", src: url, alt: "" }));
    }
    preview.append(el("span", { className: imagePreview ? "attachment-preview-chip has-preview" : "attachment-preview-chip", title: `Download ${filename}` }, [
      el("a", { className: "attachment-chip-name", href: url, download: filename }, linkContent),
      remove
    ]));
  });
  preview.attachmentPreviewURLs = urls;
}

function isStagedImageFile(file) {
  return String(file?.type || "").toLowerCase().startsWith("image/");
}

function cleanupAttachmentPreview(preview) {
  for (const url of preview.attachmentPreviewURLs || []) {
    URL.revokeObjectURL(url);
  }
  preview.attachmentPreviewURLs = [];
}

function mergeAttachmentFiles(...groups) {
  const seen = new Set();
  const files = [];
  for (const file of groups.flat()) {
    const key = [file.name, file.size, file.lastModified, file.type].join("\0");
    if (seen.has(key)) continue;
    seen.add(key);
    files.push(file);
  }
  return files;
}

function appendAttachmentFiles(input, files) {
  setAttachmentFiles(input, mergeAttachmentFiles(Array.from(input.files || []), Array.from(files || [])));
}

const handledAttachmentDropEvents = new WeakSet();

export function bindAttachmentDropZone(target, input, activeClass = "attachment-drop-active") {
  let cleanupTimer = 0;
  const hasFiles = (event) => Array.from(event.dataTransfer?.types || []).includes("Files");
  const deactivate = () => {
    window.clearTimeout(cleanupTimer);
    cleanupTimer = 0;
    target.classList.remove(activeClass);
  };
  const scheduleCleanup = () => {
    window.clearTimeout(cleanupTimer);
    cleanupTimer = window.setTimeout(deactivate, 5000);
  };
  const activate = (event) => {
    if (!hasFiles(event)) return false;
    event.preventDefault();
    target.classList.add(activeClass);
    scheduleCleanup();
    return true;
  };
  target.addEventListener("dragenter", (event) => {
    activate(event);
  });
  target.addEventListener("dragover", (event) => {
    if (!activate(event)) return;
    event.dataTransfer.dropEffect = "copy";
  });
  target.addEventListener("dragleave", (event) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    if (event.relatedTarget && target.contains(event.relatedTarget)) return;
    deactivate();
  });
  target.addEventListener("drop", (event) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    deactivate();
    if (handledAttachmentDropEvents.has(event)) return;
    handledAttachmentDropEvents.add(event);
    if (event.dataTransfer?.files?.length) appendAttachmentFiles(input, event.dataTransfer.files);
  });
}

export function bindAttachmentPasteZone(target, input) {
  target.addEventListener("paste", (event) => {
    const files = clipboardAttachmentFiles(event.clipboardData);
    if (files.length === 0) return;
    event.preventDefault();
    appendAttachmentFiles(input, files);
  });
}

function clipboardAttachmentFiles(clipboardData) {
  const files = [];
  for (const item of Array.from(clipboardData?.items || [])) {
    if (item.kind !== "file") continue;
    const file = item.getAsFile();
    if (file) files.push(file);
  }
  if (files.length > 0) return files;
  return Array.from(clipboardData?.files || []);
}

function setAttachmentFiles(input, files) {
  const maxFiles = Number(state.meta.uploads?.max_files || 0);
  const selected = maxFiles > 0 ? files.slice(0, maxFiles) : files;
  const transfer = new DataTransfer();
  for (const file of selected) transfer.items.add(file);
  input.files = transfer.files;
  input.dispatchEvent(new Event("change", { bubbles: true }));
}

export function selectedTicketFiles(form) {
  if (!form) return [];
  const files = [];
  form.querySelectorAll("input[type='file']").forEach((input) => {
    files.push(...Array.from(input.files || []));
  });
  return files;
}

export function selectedCommentFiles(composer) {
  if (!composer) return [];
  const files = [];
  composer.querySelectorAll("input[type='file']").forEach((input) => {
    files.push(...Array.from(input.files || []));
  });
  return files;
}

export function attachmentList(attachments) {
  if (!attachments || attachments.length === 0) return el("div", { className: "attachment-list empty" });
  const list = el("div", { className: "attachment-list" });
  const previews = attachments.filter(isPreviewableImageAttachment);
  if (previews.length > 0) {
    list.append(el("div", { className: "attachment-preview-grid" }, previews.map(imageAttachmentPreview)));
  }
  for (const attachment of attachments) {
    list.append(el("a", {
      className: "attachment-link",
      href: `/api/attachments/${attachment.id}`,
      download: attachment.filename
    }, [
      el("span", { className: "attachment-name" }, attachment.filename || "Attachment"),
      el("span", { className: "attachment-size" }, formatBytes(attachment.size_bytes || 0))
    ]));
  }
  return list;
}

function isPreviewableImageAttachment(attachment) {
  return ["image/png", "image/jpeg", "image/gif", "image/webp"].includes(String(attachment.content_type || "").toLowerCase());
}

function imageAttachmentPreview(attachment) {
  const url = `/api/attachments/${attachment.id}`;
  const filename = attachment.filename || "Attached image";
  const button = el("button", {
    className: "attachment-image-link",
    type: "button",
    title: filename
  }, [
    el("img", {
      className: "attachment-image-preview",
      src: `${url}?preview=1`,
      alt: filename,
      loading: "lazy",
      decoding: "async"
    })
  ]);
  button.addEventListener("click", () => openImagePreview({ src: `${url}?preview=1`, filename }));
  return button;
}

let imagePreviewDialog = null;

function openImagePreview({ src, filename }) {
  const dialog = getImagePreviewDialog();
  const image = dialog.querySelector(".image-preview-modal-image");
  const title = dialog.querySelector(".image-preview-modal-title");
  image.src = src;
  image.alt = filename;
  title.textContent = filename;
  if (!dialog.open) dialog.showModal();
}

function getImagePreviewDialog() {
  if (imagePreviewDialog) return imagePreviewDialog;
  const close = el("button", {
    className: "image-preview-modal-close",
    type: "button",
    "aria-label": "Close image preview"
  }, "x");
  const dialog = el("dialog", { className: "image-preview-modal", "aria-label": "Image preview" }, [
    el("div", { className: "image-preview-modal-shell" }, [
      el("header", { className: "image-preview-modal-header" }, [
        el("strong", { className: "image-preview-modal-title" }, ""),
        close
      ]),
      el("img", { className: "image-preview-modal-image", alt: "" })
    ])
  ]);
  close.addEventListener("click", () => dialog.close());
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) dialog.close();
  });
  dialog.addEventListener("close", () => {
    const image = dialog.querySelector(".image-preview-modal-image");
    image.removeAttribute("src");
    image.alt = "";
  });
  document.body.append(dialog);
  imagePreviewDialog = dialog;
  return dialog;
}

export function formatBytes(value) {
  const bytes = Number(value || 0);
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
