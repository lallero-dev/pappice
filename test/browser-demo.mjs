#!/usr/bin/env node
import assert from "node:assert/strict";
import { mkdtemp, readFile, readdir, rename, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { buildBrowser } from "../scripts/build-browser.mjs";
import { serveBrowser } from "../scripts/serve-browser.mjs";
import { findChromium } from "./tools/chromium.mjs";
import { connectToPage, freePort, repoRoot, startChromium, stopProcess } from "./tools/local-pappice.mjs";
import { runInPage, waitForDocumentReady } from "./tools/browser-page.mjs";

const directory = await mkdtemp(path.join(tmpdir(), "pappice-browser-"));
const requests = [];
let server;
let chrome;
let page;
let secondPage;
try {
  // A subdirectory exercises static hosting without root routes or SPA rewrites.
  await buildBrowser(path.join(directory, "site/demo"));
  for (const file of await readdir(path.join(repoRoot, "internal/server/web/static"))) {
    assert.deepEqual(
      await readFile(path.join(directory, "site/demo/static", file)),
      await readFile(path.join(repoRoot, "internal/server/web/static", file)),
      `Browser build must preserve the shared asset ${file}`
    );
  }
  server = await serveBrowser(path.join(directory, "site"), { port: 0, onRequest: (url) => requests.push(url) });
  const appURL = `http://127.0.0.1:${server.address().port}/demo/`;
  for (const [accept, encoding] of [["gzip", "gzip"], ["gzip;q=0, *;q=1", null], ["identity", null]]) {
    const response = await fetch(`${appURL}pappice.wasm`, { method: "HEAD", headers: { "Accept-Encoding": accept } });
    assert.equal(response.status, 200);
    assert.equal(response.headers.get("Content-Encoding"), encoding);
  }
  const compressed = path.join(directory, "site/demo/pappice.wasm.gz");
  await rename(compressed, `${compressed}.tmp`);
  const uncompressed = await fetch(`${appURL}pappice.wasm`, { method: "HEAD", headers: { "Accept-Encoding": "gzip" } });
  assert.equal(uncompressed.status, 200, "Static host must work without a precompressed WASM file");
  assert.equal(uncompressed.headers.get("Content-Encoding"), null);
  await rename(`${compressed}.tmp`, compressed);
  const port = await freePort();
  chrome = startChromium({ appURL, port, chromiumPath: findChromium(), userDataDir: path.join(directory, "chrome") });
  page = await connectToPage(port, appURL);
  await page.send("Page.enable");
  await page.send("Runtime.enable");
  await waitForDocumentReady(page);
  console.log("Static demo loaded; waiting for Go and SQLite.");
  await runInPage(page, async () => {
    const { waitFor } = pageTools();
    await waitFor(() => !document.querySelector("#authView").hidden, "browser demo login", 25000);
    document.querySelector("#loginForm").requestSubmit();
    await waitFor(() => !document.querySelector("#appView").hidden, "browser demo signed in", 20000);
    await waitFor(() => document.querySelector("#ticketList")?.textContent.includes("Login page"), "seeded ticket list", 20000);
    return true;
  });
  console.log("Admin login and seeded tickets work.");
  await runInPage(page, async () => {
    const { modalRoot, openModalRoot, setValue, waitFor } = pageTools();
    document.querySelector("#newTicketButton").click();
    const modal = await waitFor(() => {
      const root = modalRoot();
      return root?.querySelector(".ticket-create-flow [name='title']") ? root : null;
    }, "new ticket dialog");
    await waitFor(() => modal.querySelector("link[rel='stylesheet']").sheet?.cssRules.length, "dialog stylesheet in subdirectory");
    const product = modal.querySelector("[name='product_id']");
    if (!product.value) setValue(product, [...product.options].find((option) => option.value).value);
    setValue(modal.querySelector("[name='priority']"), "normal");
    setValue(modal.querySelector("[name='title']"), "Created through the browser UI");
    setValue(modal.querySelector("[name='description']"), "UI stays connected to the local Go backend.");
    if (!modal.querySelector(".attachment-input")) throw new Error("Attachment control was hidden");
    modal.querySelector("footer .primary").click();
    const confirm = await waitFor(() => openModalRoot("Create this ticket?"), "ticket confirmation");
    confirm.querySelector("footer .primary").click();
    await waitFor(() => !modalRoot()?.querySelector("dialog[open]"), "ticket saved", 12000);
    await waitFor(() => document.querySelector("#ticketDetailPane").textContent.includes("UI stays connected"), "new ticket conversation");
    const composer = document.querySelector(".comment-form");
    setValue(composer.querySelector(".comment-input"), "Reply through the browser UI");
    composer.querySelector("[data-comment-send]").click();
    const reply = await waitFor(() => openModalRoot("Send this reply?"), "reply confirmation");
    reply.querySelector("footer .primary").click();
    await waitFor(() => document.querySelector(".conversation-stream").textContent.includes("Reply through the browser UI"), "reply saved");
    return true;
  });
  const result = await runInPage(page, async () => {
    const { request } = await import("./static/api.js");
    const { state } = await import("./static/state.js");
    const { platform } = await import("./static/platform.js");
    const [products, health, webhooks] = await Promise.all([
      request("/api/products"), request("/api/health"), platform.fetch("/api/webhooks")
    ]);
    const ticket = await request("/api/tickets", { method: "POST", body: JSON.stringify({
      product_id: products.products[0].id, title: "Created inside WebAssembly", description: "Private data", priority: "high"
    }) });
    await request(`/api/tickets/${ticket.id}/comments`, { method: "POST", body: JSON.stringify({ body: "Real Go reply", visibility: "public" }) });
    const updated = await request(`/api/tickets/${ticket.id}`);
    const multipart = new FormData();
    multipart.append("attachments", new Blob(["demo"]), "demo.txt");
    const attachments = await platform.fetch("/api/tickets", { method: "POST", body: multipart });
    document.querySelector("#adminTab").click();
    const { waitFor } = pageTools();
    await waitFor(() => document.querySelector("#userList").textContent.includes("Alex Admin"), "admin accounts");
    return {
      title: updated.title, comments: updated.comments.map((comment) => comment.body),
      csrf: Boolean(state.csrf), webhooks: webhooks.status, attachments: attachments.status,
      email: health.email_enabled, hash: location.hash,
      visibleSections: [...document.querySelectorAll('[data-admin-section]')].filter((node) => !node.hidden).map((node) => node.dataset.adminSection)
    };
  });
  assert.equal(result.title, "Created inside WebAssembly");
  assert(result.comments.includes("Real Go reply"));
  assert(result.csrf);
  assert.equal(result.webhooks, 200);
  assert.equal(result.attachments, 501);
  assert.equal(result.email, false);
  assert.equal(result.hash, "#/admin/accounts");
  assert.deepEqual(result.visibleSections, ["accounts", "tokens", "webhooks", "email", "maintenance", "audit"]);
  assert(!requests.some((url) => url.includes("/api/")), "API requests must never reach the static host");
  console.log("Ticket creation, reply, account management and local API passed.");

  const sections = await runInPage(page, async () => {
    const { request } = await import("./static/api.js");
    const { waitFor } = pageTools();
    const created = await request("/api/webhooks", { method: "POST", body: JSON.stringify({
      name: "Local integration", url: "https://example.test/hook", events: ["ticket.created"], enabled: true
    }) });
    document.querySelector('[data-admin-section="webhooks"]').click();
    await waitFor(() => document.querySelector("#globalWebhookList").textContent.includes("Local integration"), "webhook configuration");
    let deliveryError;
    try {
      await request(`/api/webhooks/${created.webhook.id}/test`, { method: "POST" });
    } catch (error) {
      deliveryError = { status: error.status, message: error.message };
    }
    document.querySelector('[data-admin-section="maintenance"]').click();
    await waitFor(() => document.querySelector("#maintenanceOverview").textContent.includes(":memory:"), "maintenance data");
    const maintenance = await request("/api/admin/maintenance");
    const products = await request("/api/products");
    const id = products.products[0].id;
    location.hash = `/products/${id}/webhooks`;
    await waitFor(() => !document.querySelector("#productView").hidden && document.querySelector("#webhookList").textContent.includes("No webhooks"), "product webhooks");
    const visibleProductSections = [...document.querySelectorAll("[data-product-section]")].filter((node) => !node.hidden).map((node) => node.dataset.productSection);
    document.querySelector('[data-product-section="deliveries"]').click();
    await waitFor(() => document.querySelector("#deliveryList").textContent.includes("No deliveries"), "product delivery history");
    return { deliveryError, databaseSize: maintenance.database_size_bytes, attachmentSize: maintenance.attachment_storage_bytes, visibleProductSections };
  });
  assert.equal(sections.deliveryError.status, 501);
  assert.match(sections.deliveryError.message, /Outgoing webhook delivery is unavailable/);
  assert(sections.databaseSize > 0);
  assert.equal(sections.attachmentSize, 0);
  assert.deepEqual(sections.visibleProductSections, ["general", "members", "webhooks", "deliveries"]);
  console.log("Every section remains visible; webhook configuration, delivery history and maintenance work.");

  const secondURL = `${appURL}?instance=2`;
  const secondTarget = await page.send("Target.createTarget", { url: secondURL });
  secondPage = await connectToPage(port, secondURL);
  await secondPage.send("Page.enable");
  await secondPage.send("Runtime.enable");
  await waitForDocumentReady(secondPage);
  const isolated = await runInPage(secondPage, async () => {
    const { waitFor } = pageTools();
    await waitFor(() => !document.querySelector("#authView").hidden, "independent tab login", 25000);
    const { request } = await import("./static/api.js");
    const session = await request("/api/session");
    document.querySelector("#loginForm").requestSubmit();
    await waitFor(() => !document.querySelector("#appView").hidden, "second tab sign in", 20000);
    const tickets = await request("/api/tickets?q=Created");
    return { authenticated: session.authenticated, tickets: tickets.tickets.length };
  });
  assert.deepEqual(isolated, { authenticated: false, tickets: 0 });
  await secondPage.close();
  secondPage = null;
  await page.send("Target.closeTarget", { targetId: secondTarget.targetId });
  console.log("Two tabs on the same origin have independent data and sessions.");

  await page.send("Network.enable");
  await page.send("Network.emulateNetworkConditions", { offline: true, latency: 0, downloadThroughput: 0, uploadThroughput: 0 });
  const offline = await runInPage(page, async () => {
    const { request } = await import("./static/api.js");
    const payload = await request("/api/tickets?q=Created%20inside%20WebAssembly");
    const ticket = payload.tickets[0];
    await request(`/api/tickets/${ticket.id}/comments`, { method: "POST", body: JSON.stringify({ body: "Works offline", visibility: "internal" }) });
    const updated = await request(`/api/tickets/${ticket.id}`);
    return updated.comments.some((comment) => comment.body === "Works offline");
  });
  assert(offline);
  await page.send("Network.emulateNetworkConditions", { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  console.log("Reading and writing tickets also works with networking offline.");

  const customer = await runInPage(page, async () => {
    const { setValue, waitFor } = pageTools();
    document.querySelector("#logoutButton").click();
    await waitFor(() => !document.querySelector("#authView").hidden, "logout");
    const form = document.querySelector("#loginForm");
    setValue(form.elements.email, "customer@example.test");
    setValue(form.elements.password, "pappice-demo");
    form.requestSubmit();
    await waitFor(() => !document.querySelector("#appView").hidden, "customer sign in", 20000);
    const { request } = await import("./static/api.js");
    const { platform } = await import("./static/platform.js");
    const admin = await platform.fetch("/api/tokens");
    const products = await request("/api/products");
    return { forbidden: admin.status, adminHidden: document.querySelector("#adminTab").hidden, products: products.products.length };
  });
  assert.deepEqual(customer, { forbidden: 403, adminHidden: true, products: 1 });

  await page.send("Page.reload", { ignoreCache: false });
  await waitForDocumentReady(page);
  const reset = await runInPage(page, async () => {
    const { waitFor } = pageTools();
    await waitFor(() => !document.querySelector("#authView").hidden, "login after reset", 25000);
    document.querySelector("#loginForm").requestSubmit();
    await waitFor(() => !document.querySelector("#appView").hidden, "login after reset", 20000);
    const { request } = await import("./static/api.js");
    const payload = await request("/api/tickets?q=Created%20inside%20WebAssembly");
    return payload.tickets.length;
  });
  assert.equal(reset, 0);
  console.log("Role permissions and reload reset passed.");
  const invalidRoute = await runInPage(page, async () => {
    const { waitFor } = pageTools();
    location.hash = "/products";
    await waitFor(() => !document.querySelector("#productView").hidden, "product route");
    location.hash = "http://%";
    await waitFor(() => !document.querySelector("#ticketView").hidden, "recover from malformed route");
    const { platform } = await import("./static/platform.js");
    return platform.location().pathname;
  });
  assert.equal(invalidRoute, "/tickets");
  const setup = await runInPage(page, async () => {
    const { request } = await import("./static/api.js");
    const user = await request("/api/users", { method: "POST", body: JSON.stringify({ email: "new@example.test", display_name: "New Browser User", role: "customer" }) });
    const link = user.account_link.url;
    if (!link.includes("/demo/#/account/setup/")) throw new Error(`Invalid local account link: ${link}`);
    location.href = link;
    const { setValue, waitFor } = pageTools();
    await waitFor(() => !document.querySelector("#accountLinkForm").hidden, "account link hash route");
    const form = document.querySelector("#accountLinkForm");
    await waitFor(() => !form.querySelector("button[type='submit']").disabled, "account link resolved");
    history.back();
    await waitFor(() => !document.querySelector("#appView").hidden, "return from account setup");
    if (document.querySelector("#profileName").textContent !== "Alex Admin") throw new Error("Account link navigation lost the session");
    location.href = link;
    await waitFor(() => !document.querySelector("#authView").hidden && !form.querySelector("button[type='submit']").disabled, "reopen account setup");
    setValue(form.elements.password, "browser-account-password");
    form.requestSubmit();
    await waitFor(() => !document.querySelector("#appView").hidden, "account setup completed", 20000);
    return document.querySelector("#profileName").textContent;
  });
  assert.equal(setup, "New Browser User");
  assert(!requests.some((url) => url.includes("/api/")), "API requests leaked to the static host");
  assert(requests.includes("/demo/static/components.css"), "Dialog stylesheet did not load from its module directory");
  assert(!requests.some((url) => url.startsWith("/static/")), "Static assets escaped the deployment subdirectory");
  console.log("One-time account setup and routing work inside the same tab.");

  const blockedWorker = await page.send("Page.addScriptToEvaluateOnNewDocument", {
    source: 'globalThis.Worker = class { constructor() { throw new DOMException("Workers blocked", "SecurityError"); } };'
  });
  await page.send("Page.reload");
  await waitForDocumentReady(page);
  const startupError = await runInPage(page, async () => {
    const { waitFor } = pageTools();
    await waitFor(() => document.querySelector("#browserStatus").getAttribute("role") === "alert", "worker startup failure");
    return document.querySelector("#browserStatus").textContent;
  });
  assert.match(startupError, /Workers blocked/);
  await page.send("Page.removeScriptToEvaluateOnNewDocument", { identifier: blockedWorker.identifier });
  await rename(compressed, `${compressed}.tmp`);
  const wasm = path.join(directory, "site/demo/pappice.wasm");
  await rename(wasm, `${wasm}.tmp`);
  await page.send("Page.reload", { ignoreCache: true });
  await waitForDocumentReady(page);
  const downloadError = await runInPage(page, async () => {
    const { waitFor } = pageTools();
    await waitFor(() => document.querySelector("#browserStatus").getAttribute("role") === "alert", "WASM download failure");
    return document.querySelector("#browserStatus").textContent;
  });
  assert.match(downloadError, /Could not load Pappice \(404\)/);
  console.log("Startup failures are visible. Browser demo test passed.");
} catch (error) {
  if (page) {
    console.error(await runInPage(page, () => ({ status: document.querySelector("#browserStatus")?.textContent, text: document.body.innerText.slice(0, 2000) })).catch(() => "Page unavailable"));
  }
  throw error;
} finally {
  if (page) await page.close();
  if (secondPage) await secondPage.close();
  if (chrome) await stopProcess(chrome);
  if (server) await new Promise((resolve) => server.close(resolve));
  await rm(directory, { recursive: true, force: true, maxRetries: 3 });
}
