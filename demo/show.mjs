#!/usr/bin/env node

import { mkdir, rm, writeFile } from "node:fs/promises";
import https from "node:https";
import path from "node:path";

import { runInPage, waitForDocumentReady } from "../test/tools/browser-page.mjs";
import { repoRoot, runCommand, startLocalPappice } from "../test/tools/local-pappice.mjs";

const fps = 10;
const viewport = { width: 1180, height: 760 };
const frameRoot = path.join(repoRoot, "demo", ".tmp");
const framesDir = path.join(frameRoot, "frames");
const outputPath = path.resolve(repoRoot, process.env.PAPPICE_DEMO_OUTPUT || "assets/demo.gif");

let app = null;
let frameIndex = 0;

main().catch(async (error) => {
  console.error("");
  console.error("Demo GIF generation failed:");
  console.error(error?.stack || error);
  await cleanup();
  process.exit(1);
});

async function main() {
  await runCommand("ffmpeg", ["-version"]);
  await rm(frameRoot, { force: true, recursive: true });
  await mkdir(framesDir, { recursive: true });
  await mkdir(path.dirname(outputPath), { recursive: true });

  app = await startLocalPappice({
    emailNotifications: false,
    keepTemp: Boolean(process.env.PAPPICE_DEMO_KEEP_TMP),
    tempPrefix: "pappice-demo-",
    viewport
  });

  const { page } = app;
  await page.send("Emulation.setDeviceMetricsOverride", {
    deviceScaleFactor: 1,
    height: viewport.height,
    mobile: false,
    width: viewport.width
  });
  await waitForDocumentReady(page);

  const demo = await seedDemoData(page);
  await showLogin(page);
  await hold(page, 1.6);

  await fillLogin(page, demo.customer);
  await hold(page, 1.1);

  await submitLogin(page, demo.customer);
  await hold(page, 1.4);

  await openNewTicketModal(page);
  await hold(page, 1.1);

  await fillTicketProduct(page, demo.product);
  await hold(page, 0.8);

  await fillTicketPriority(page, "high");
  await hold(page, 0.8);

  const ticket = {
    description: "Hi, I cannot access the billing dashboard after the last update.\nIt shows a blank page after login.",
    followUp: "I also tried from mobile and get the same blank page.",
    title: "Cannot access the billing dashboard"
  };
  await fillTicketIssue(page, ticket);
  await hold(page, 1.3);

  await openCreateConfirmation(page);
  await hold(page, 1.0);

  const ticketKey = await confirmTicketCreate(page, ticket);
  await hold(page, 1.6);

  await typeReply(page, ticket.followUp);
  await attachFakeScreenshot(page);
  await hold(page, 1.4);

  await openSendConfirmation(page);
  await hold(page, 0.9);

  await confirmReply(page, ticket.followUp);
  await hold(page, 1.6);

  const staffReply = "Thanks, we found the broken redirect. The dashboard should load correctly now.";
  await postStaffReply(demo.admin, ticketKey, staffReply);
  await refreshTicket(page, ticketKey, staffReply);
  await hold(page, 2.0);

  const finalReply = "I tried, works, thanks.";
  await typeReply(page, finalReply);
  await hold(page, 0.9);

  await openSendConfirmation(page);
  await hold(page, 0.8);

  await confirmReply(page, finalReply);
  await hold(page, 4.0);

  await encodeGif();
  await cleanup();
  console.log(`Generated ${path.relative(repoRoot, outputPath)}`);
}

async function seedDemoData(page) {
  return runInPage(page, async () => {
    const admin = {
      displayName: "Support Agent",
      email: "support@example.test",
      password: "pappice-demo-admin"
    };
    const customer = {
      displayName: "Sample Customer",
      email: "customer@example.test",
      password: "pappice-demo-customer"
    };
    let csrf = "";

    async function api(url, options = {}) {
      const method = options.method || "GET";
      const headers = { Accept: "application/json" };
      if (options.body !== undefined) headers["Content-Type"] = "application/json";
      if (csrf && method !== "GET") headers["X-Pappice-CSRF"] = csrf;
      const response = await fetch(url, {
        body: options.body === undefined ? undefined : JSON.stringify(options.body),
        credentials: "same-origin",
        headers,
        method
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(`${method} ${url} failed with ${response.status}: ${payload.error || "no response body"}`);
      }
      if (payload.csrf_token) csrf = payload.csrf_token;
      return payload;
    }

    await api("/api/setup", {
      method: "POST",
      body: {
        display_name: admin.displayName,
        email: admin.email,
        password: admin.password,
        role: "admin"
      }
    });

    const product = await api("/api/products", {
      method: "POST",
      body: {
        description: "Customer requests for the billing area.",
        key: "BILLING",
        name: "Billing Portal"
      }
    });

    const createdCustomer = await api("/api/users", {
      method: "POST",
      body: {
        display_name: customer.displayName,
        email: customer.email,
        password: customer.password,
        role: "customer"
      }
    });
    await api(`/api/products/${product.id}/members`, {
      method: "POST",
      body: { role: "customer", user_id: createdCustomer.id }
    });
    await api("/api/logout", { method: "POST" });

    return { admin, customer, product: { id: product.id, name: product.name } };
  });
}

async function showLogin(page) {
  await page.send("Page.navigate", { url: app.appURL });
  await waitForDocumentReady(page);
  await runInPage(page, async () => {
    const { waitFor } = pageTools();
    await waitFor(() => {
      const form = document.querySelector("#loginForm");
      return form && !form.hidden ? form : null;
    }, "demo login form");
    return true;
  });
}

async function fillLogin(page, customer) {
  await runInPage(page, async (input) => {
    const { setValue, waitFor } = pageTools();
    const form = await waitFor(() => {
      const candidate = document.querySelector("#loginForm");
      return candidate && !candidate.hidden ? candidate : null;
    }, "demo visible login form");
    setValue(form.querySelector("[name='email']"), input.email);
    setValue(form.querySelector("[name='password']"), input.password);
    return true;
  }, customer);
}

async function submitLogin(page, customer) {
  await runInPage(page, async (input) => {
    const { waitFor } = pageTools();
    document.querySelector("#loginForm").requestSubmit();
    await waitFor(() => {
      const app = document.querySelector("#appView");
      return app && !app.hidden && document.querySelector("#profileName")?.textContent.includes(input.displayName);
    }, "demo customer session", 12000);
    return true;
  }, customer);
}

async function openNewTicketModal(page) {
  await runInPage(page, async () => {
    const { modalRoot, waitFor } = pageTools();
    const button = await waitFor(() => {
      const candidate = document.querySelector("#newTicketButton");
      return candidate && !candidate.hidden ? candidate : null;
    }, "demo new ticket button");
    button.click();
    await waitFor(() => {
      const root = modalRoot();
      return root?.querySelector("dialog[open] h2")?.textContent.includes("New Ticket") ? root : null;
    }, "demo new ticket modal");
    return true;
  });
}

async function fillTicketProduct(page, product) {
  await runInPage(page, async (input) => {
    const { modalRoot, setValue, waitFor } = pageTools();
    const select = await waitFor(() => modalRoot()?.querySelector(".ticket-create-flow [name='product_id']"), "demo product select");
    const option = [...select.options].find((item) => Number(item.value) === input.id) ||
      [...select.options].find((item) => item.value);
    setValue(select, option.value);
    await waitFor(() => !modalRoot()?.querySelector(".ticket-create-flow [name='priority']")?.disabled, "demo priority step enabled");
    return true;
  }, product);
}

async function fillTicketPriority(page, priority) {
  await runInPage(page, async (input) => {
    const { modalRoot, setValue, waitFor } = pageTools();
    const select = await waitFor(() => modalRoot()?.querySelector(".ticket-create-flow [name='priority']"), "demo priority select");
    setValue(select, input.priority);
    await waitFor(() => !modalRoot()?.querySelector(".ticket-create-flow [name='title']")?.disabled, "demo issue step enabled");
    return true;
  }, { priority });
}

async function fillTicketIssue(page, ticket) {
  await runInPage(page, async (input) => {
    const { modalRoot, setValue, waitFor } = pageTools();
    const root = await waitFor(() => modalRoot()?.querySelector(".ticket-create-flow"), "demo ticket create flow");
    setValue(root.querySelector("[name='title']"), input.title);
    setValue(root.querySelector("[name='description']"), input.description);
    await waitFor(() => !modalRoot()?.querySelector("footer .primary")?.disabled, "demo create button enabled");
    return true;
  }, ticket);
}

async function openCreateConfirmation(page) {
  await runInPage(page, async () => {
    const { openModalRoot, waitFor } = pageTools();
    document.querySelector("#modalHost").shadowRoot.querySelector("footer .primary").click();
    await waitFor(() => openModalRoot("Create this ticket?"), "demo create confirmation");
    return true;
  });
}

async function confirmTicketCreate(page, ticket) {
  return runInPage(page, async (input) => {
    const { openModalRoot, waitFor } = pageTools();
    openModalRoot("Create this ticket?").querySelector("footer .primary").click();
    await waitFor(() => {
      const pane = document.querySelector("#ticketDetailPane");
      return pane?.textContent.includes(input.title) &&
        pane.textContent.includes(input.description.split("\n")[0]) &&
        document.querySelector("#ticketList .ticket-row.active");
    }, "demo created ticket detail", 12000);
    await waitFor(() => /^#[A-Z][A-Z0-9]{1,15}-[1-9][0-9]*$/.test(window.location.hash), "demo ticket hash");
    return decodeURIComponent(window.location.hash.slice(1));
  }, ticket);
}

async function typeReply(page, body) {
  await runInPage(page, async (input) => {
    const { setValue, waitFor } = pageTools();
    const textarea = await waitFor(() => document.querySelector("#ticketDetailPane [name='body']"), "reply textarea");
    setValue(textarea, input.body);
    await waitFor(() => !document.querySelector("#ticketDetailPane [data-comment-send]")?.disabled, "enabled send button");
    return true;
  }, { body });
}

async function attachFakeScreenshot(page) {
  await runInPage(page, async () => {
    const { pasteFiles, waitFor } = pageTools();
    const textarea = await waitFor(() => document.querySelector("#ticketDetailPane [name='body']"), "reply textarea");
    const canvas = document.createElement("canvas");
    canvas.width = 640;
    canvas.height = 360;
    const context = canvas.getContext("2d");
    context.fillStyle = "#f4f7fb";
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.fillStyle = "#243849";
    context.fillRect(0, 0, canvas.width, 54);
    context.fillStyle = "#ffffff";
    context.font = "bold 22px sans-serif";
    context.fillText("Billing dashboard", 28, 36);
    context.fillStyle = "#ffffff";
    context.fillRect(42, 92, 556, 210);
    context.strokeStyle = "#c8d5e3";
    context.lineWidth = 2;
    context.strokeRect(42, 92, 556, 210);
    context.fillStyle = "#d92d20";
    context.beginPath();
    context.arc(318, 175, 28, 0, Math.PI * 2);
    context.fill();
    context.fillStyle = "#ffffff";
    context.font = "bold 34px sans-serif";
    context.fillText("!", 309, 187);
    context.fillStyle = "#243849";
    context.font = "bold 20px sans-serif";
    context.fillText("Blank page after login", 218, 236);
    context.fillStyle = "#5c6f85";
    context.font = "16px sans-serif";
    context.fillText("screenshot attached by customer", 218, 264);
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, "image/png"));
    pasteFiles(textarea, [new File([blob], "billing-dashboard.png", { type: "image/png" })]);
    await waitFor(() => {
      const composer = document.querySelector("#ticketDetailPane .comment-form");
      return composer?.querySelector(".attachment-preview-chip.has-preview")?.textContent.includes("billing-dashboard.png");
    }, "demo screenshot attachment chip");
    return true;
  });
}

async function openSendConfirmation(page) {
  await runInPage(page, async () => {
    const { openModalRoot, waitFor } = pageTools();
    document.querySelector("#ticketDetailPane [data-comment-send]").click();
    await waitFor(() => openModalRoot("Send this reply?"), "send confirmation");
    return true;
  });
}

async function confirmReply(page, body) {
  await runInPage(page, async (input) => {
    const { openModalRoot, waitFor } = pageTools();
    openModalRoot("Send this reply?").querySelector("footer .primary").click();
    await waitFor(() => {
      const pane = document.querySelector("#ticketDetailPane");
      return pane?.textContent.includes(input.body) &&
        pane.querySelector(".attachment-image-preview[alt='billing-dashboard.png']");
    }, "sent demo reply with screenshot", 12000);
    return true;
  }, { body });
}

async function postStaffReply(admin, ticketKey, body) {
  const api = createAPIClient(app.appURL);
  await api("/api/login", {
    method: "POST",
    body: { email: admin.email, password: admin.password }
  });
  const ticket = await api(`/api/tickets/key/${encodeURIComponent(ticketKey)}`);
  await api(`/api/tickets/${ticket.id}/comments`, {
    method: "POST",
    body: { body, visibility: "public" }
  });
}

async function refreshTicket(page, ticketKey, expectedText) {
  await page.send("Page.navigate", { url: `${app.appURL}/tickets#${encodeURIComponent(ticketKey)}` });
  await page.send("Page.reload", { ignoreCache: true });
  await waitForDocumentReady(page);
  await runInPage(page, async (input) => {
    const { waitFor } = pageTools();
    await waitFor(() => {
      const pane = document.querySelector("#ticketDetailPane");
      return pane?.textContent.includes(input.expectedText) &&
        pane.querySelector(".attachment-image-preview[alt='billing-dashboard.png']");
    }, "demo staff reply visible to customer", 12000);
    return true;
  }, { expectedText });
}

function createAPIClient(baseURL) {
  let csrf = "";
  const cookies = new Map();
  return async function api(pathname, options = {}) {
    const method = options.method || "GET";
    const body = options.body === undefined ? null : JSON.stringify(options.body);
    const target = new URL(pathname, baseURL);
    const headers = {
      Accept: "application/json"
    };
    if (body !== null) headers["Content-Type"] = "application/json";
    if (method !== "GET") {
      headers.Origin = baseURL;
      if (csrf) headers["X-Pappice-CSRF"] = csrf;
    }
    if (cookies.size > 0) {
      headers.Cookie = [...cookies.entries()].map(([name, value]) => `${name}=${value}`).join("; ");
    }
    const response = await httpsRequest(target, { body, headers, method });
    for (const header of response.setCookie) {
      const [pair] = header.split(";");
      const separator = pair.indexOf("=");
      if (separator > -1) cookies.set(pair.slice(0, separator), pair.slice(separator + 1));
    }
    const payload = response.body ? JSON.parse(response.body) : {};
    if (!response.ok) {
      throw new Error(`${method} ${pathname} failed with ${response.statusCode}: ${payload.error || response.body || "no response body"}`);
    }
    if (payload.csrf_token) csrf = payload.csrf_token;
    return payload;
  };
}

function httpsRequest(target, { body, headers, method }) {
  return new Promise((resolve, reject) => {
    const request = https.request(target, {
      headers,
      method,
      rejectUnauthorized: false
    }, (response) => {
      const chunks = [];
      response.on("data", (chunk) => chunks.push(Buffer.from(chunk)));
      response.on("end", () => {
        resolve({
          body: Buffer.concat(chunks).toString("utf8"),
          ok: response.statusCode >= 200 && response.statusCode < 300,
          setCookie: response.headers["set-cookie"] || [],
          statusCode: response.statusCode || 0
        });
      });
    });
    request.on("error", reject);
    if (body !== null) request.write(body);
    request.end();
  });
}

async function hold(page, seconds) {
  const frames = Math.max(1, Math.round(seconds * fps));
  for (let index = 0; index < frames; index += 1) {
    await captureFrame(page);
    await sleep(1000 / fps);
  }
}

async function captureFrame(page) {
  const result = await page.send("Page.captureScreenshot", {
    captureBeyondViewport: false,
    format: "png",
    fromSurface: true
  });
  const filename = `frame-${String(frameIndex).padStart(4, "0")}.png`;
  frameIndex += 1;
  await writeFile(path.join(framesDir, filename), Buffer.from(result.data, "base64"));
}

async function encodeGif() {
  const scale = Math.min(viewport.width, 960);
  await runCommand("ffmpeg", [
    "-y",
    "-framerate", String(fps),
    "-i", path.join(framesDir, "frame-%04d.png"),
    "-loop", "0",
    "-vf", `fps=${fps},scale=${scale}:-1:flags=lanczos,split[s0][s1];[s0]palettegen=max_colors=128[p];[s1][p]paletteuse=dither=bayer:bayer_scale=5`,
    outputPath
  ]);
}

async function cleanup() {
  const current = app;
  app = null;
  if (current) {
    await current.cleanup();
  }
  if (!process.env.PAPPICE_DEMO_KEEP_FRAMES) {
    await rm(frameRoot, { force: true, recursive: true });
  }
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
