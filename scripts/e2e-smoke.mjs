#!/usr/bin/env node

import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import https from "node:https";
import net from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, "..");
const chromiumPath = process.env.PAPPICE_E2E_CHROMIUM || process.env.CHROMIUM || "/usr/bin/chromium";

const admin = {
  username: "admin",
  displayName: "Paolo Admin",
  email: "admin@example.test",
  password: "correct horse battery"
};

const customer = {
  username: "customer",
  displayName: "Customer One",
  email: "customer@example.test",
  password: "customer horse battery"
};

const ticket = {
  title: `E2E support request ${Date.now()}`,
  description: "The dashboard does not load for the customer smoke test.",
  reply: "Visible E2E staff reply with the next step."
};

let tempDir = "";
let appProcess = null;
let chromeProcess = null;
let smtpServer = null;
let page = null;

main().catch(async (error) => {
  console.error("");
  console.error("E2E smoke test failed:");
  console.error(error?.stack || error);
  await cleanup();
  process.exit(1);
});

async function main() {
  tempDir = await mkdtemp(path.join(tmpdir(), "pappice-e2e-"));
  const certPath = path.join(tempDir, "localhost.pem");
  const keyPath = path.join(tempDir, "localhost-key.pem");
  const dbPath = path.join(tempDir, "pappice-e2e.db");
  const binaryPath = path.join(tempDir, "pappice-e2e");

  await buildApp(binaryPath);
  await generateCertificate(certPath, keyPath);
  smtpServer = await startFakeSMTP();

  const appPort = await freePort();
  const appURL = `https://127.0.0.1:${appPort}`;
  appProcess = startApp({
    appPort,
    appURL,
    binaryPath,
    certPath,
    dbPath,
    keyPath,
    smtpPort: smtpServer.port
  });
  await waitForHTTPS(`${appURL}/api/health`, appProcess);

  const chromePort = await freePort();
  chromeProcess = startChromium(chromePort, appURL, path.join(tempDir, "chrome"));
  page = await connectToPage(chromePort);

  await page.send("Page.enable");
  await page.send("Runtime.enable");
  await waitForDocumentReady(page);

  await setupFirstAdmin(page);
  const selectedProductID = await selectFirstProduct(page);
  const setupLink = await createCustomerAccount(page);
  await addCustomerToProduct(page, selectedProductID);
  await verifyProductRouteReload(page, selectedProductID);
  await completeCustomerSetup(page, setupLink);
  const customerTicketKey = await createCustomerTicket(page);
  await verifyFixedTicketLayout(page);
  await verifyTicketHashRoute(page, customerTicketKey);
  await verifySinglePaneTicketFlow(page, customerTicketKey, {
    deviceScaleFactor: 1,
    height: 900,
    mobile: false,
    width: 820
  }, "narrow ticket");
  await verifySinglePaneTicketFlow(page, customerTicketKey, {
    deviceScaleFactor: 2,
    height: 844,
    mobile: true,
    width: 390
  }, "mobile ticket");
  await logout(page);
  await loginAsAdmin(page);
  await staffReplyAndResolve(page);
  await verifyEmailOutbox(page);
  await verifyAuditLog(page);

  await cleanup();
  console.log("E2E smoke test passed.");
}

async function setupFirstAdmin(cdp) {
  await runInPage(cdp, async (input) => {
    const { setValue, waitFor } = pageTools();
    await waitFor(() => {
      const form = document.querySelector("#setupForm");
      return form && !form.hidden;
    }, "first-run setup form");
    const form = document.querySelector("#setupForm");
    setValue(form.querySelector("[name='username']"), input.username);
    setValue(form.querySelector("[name='display_name']"), input.displayName);
    setValue(form.querySelector("[name='email']"), input.email);
    setValue(form.querySelector("[name='password']"), input.password);
    form.requestSubmit();
    await waitFor(() => {
      const app = document.querySelector("#appView");
      return app && !app.hidden && document.querySelector("#profileName")?.textContent.includes(input.displayName);
    }, "admin session after setup", 12000);
    return true;
  }, admin);
}

async function selectFirstProduct(cdp) {
  return runInPage(cdp, async () => {
    const { setValue, waitFor } = pageTools();
    const select = await waitFor(() => {
      const control = document.querySelector("#productFilter");
      const option = [...(control?.options || [])].find((item) => item.value);
      return control && option ? control : null;
    }, "product filter options");
    const firstProduct = [...select.options].find((item) => item.value);
    setValue(select, firstProduct.value);
    await waitFor(() => document.querySelector("#productTab") && !document.querySelector("#productTab").hidden, "products tab");
    return firstProduct.value;
  });
}

async function createCustomerAccount(cdp) {
  return runInPage(cdp, async (input) => {
    const { modalRoot, setValue, waitFor } = pageTools();
    document.querySelector("#adminTab").click();
    await waitFor(() => {
      const view = document.querySelector("#adminView");
      return view && !view.hidden;
    }, "admin view");
    await waitFor(() => window.location.pathname === "/admin/accounts", "admin accounts route");
    await waitFor(() => document.querySelector("#userList")?.textContent.includes("admin"), "accounts admin section");

    document.querySelector("#addUserButton").click();
    const root = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open]") ? rootNode : null;
    }, "new account modal");
    setValue(root.querySelector("[name='username']"), input.username);
    setValue(root.querySelector("[name='display_name']"), input.displayName);
    setValue(root.querySelector("[name='email']"), input.email);
    setValue(root.querySelector("[name='role']"), "customer");
    root.querySelector("form").requestSubmit();

    const linkInput = await waitFor(() => {
      const inputNode = modalRoot()?.querySelector(".link-result input");
      return inputNode?.value.includes("/account/setup/") ? inputNode : null;
    }, "customer setup link", 12000);
    const setupLink = linkInput.value;
    document.querySelector("#modalHost").close();
    await waitFor(() => !modalRoot()?.querySelector("dialog")?.open, "setup link modal closed");
    const row = await waitFor(() => {
      return [...document.querySelectorAll("#userList .admin-row")]
        .find((item) => item.textContent.includes(input.username));
    }, "customer account row");
    const rowButtons = [...row.querySelectorAll("button")].map((button) => button.textContent.trim());
    if (rowButtons.includes("Reset") || rowButtons.includes("Delete")) {
      throw new Error("reset/delete controls should be inside the edit account modal");
    }
    row.querySelector("button").click();
    const editRoot = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open] h2")?.textContent.includes(input.username) ? rootNode : null;
    }, "edit account modal");
    const reset = editRoot.querySelector("[data-account-action='reset']");
    const remove = editRoot.querySelector("[data-account-action='delete']");
    if (!reset || !remove || !remove.classList.contains("danger")) {
      throw new Error("edit account modal is missing reset/delete management actions");
    }
    reset.click();
    await waitFor(() => editRoot.querySelector(".account-confirm")?.textContent.includes("Send reset link"), "reset confirmation");
    editRoot.querySelector(".account-confirm .ghost").click();
    await waitFor(() => !editRoot.querySelector(".account-confirm"), "reset confirmation dismissed");
    remove.click();
    await waitFor(() => editRoot.querySelector(".account-confirm.danger-zone")?.textContent.includes("Delete account"), "delete confirmation");
    editRoot.querySelector(".account-confirm .ghost").click();
    await waitFor(() => !editRoot.querySelector(".account-confirm"), "delete confirmation dismissed");
    document.querySelector("#modalHost").close();
    await waitFor(() => !modalRoot()?.querySelector("dialog")?.open, "edit account modal closed");
    return setupLink;
  }, customer);
}

async function addCustomerToProduct(cdp, productID) {
  await runInPage(cdp, async ({ productID: selectedProductID, customerUsername }) => {
    const { modalRoot, setValue, waitFor } = pageTools();
    const productFilter = document.querySelector("#productFilter");
    const selectedProductLabel = [...productFilter.options].find((option) => option.value === selectedProductID)?.textContent || "";
    const separatorIndex = selectedProductLabel.indexOf(" / ");
    const selectedProductName = separatorIndex > -1 ? selectedProductLabel.slice(separatorIndex + 3) : selectedProductLabel;
    document.querySelector("#productTab").click();
    await waitFor(() => {
      const view = document.querySelector("#productView");
      return view && !view.hidden;
    }, "products view");
    await waitFor(() => window.location.pathname === "/products", "products route");
    const openButton = await waitFor(() => {
      return document.querySelector(`[data-product-open='${selectedProductID}']`);
    }, "product index open button");
    openButton.click();
    await waitFor(() => window.location.pathname === `/products/${selectedProductID}/members`, "product members route");
    await waitFor(() => {
      const title = document.querySelector("#productContextTitle")?.textContent.trim();
      return title === selectedProductName;
    }, "selected product context");
    const deleteProduct = await waitFor(() => {
      const button = document.querySelector("#deleteProductButton");
      return button && !button.hidden ? button : null;
    }, "admin product delete control");
    deleteProduct.click();
    const deleteConfirmRoot = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open] h2")?.textContent.includes("Delete this product?") ? rootNode : null;
    }, "product delete confirmation");
    if (!deleteConfirmRoot.querySelector("footer .danger")) {
      throw new Error("product deletion confirmation should use a danger action");
    }
    deleteConfirmRoot.querySelector("footer .ghost").click();
    await waitFor(() => !modalRoot()?.querySelector("dialog")?.open, "product delete confirmation dismissed");
    await waitFor(() => {
      return document.querySelector("[data-product-section='members']")?.classList.contains("active") &&
        !document.querySelector("[data-product-panel='members']")?.hidden;
    }, "product members section");

    document.querySelector("#addMemberButton").click();
    const root = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open]") ? rootNode : null;
    }, "add member modal");
    const userSelect = root.querySelector("[name='user_id']");
    const userOption = [...userSelect.options].find((option) => option.textContent.includes(customerUsername));
    if (!userOption) throw new Error(`customer ${customerUsername} missing from member account select`);
    const customerUserID = userOption.value;
    setValue(userSelect, userOption.value);
    setValue(root.querySelector("[name='role']"), "customer");
    root.querySelector("form").requestSubmit();
    await waitFor(() => !modalRoot()?.querySelector("dialog")?.open, "add member modal closed", 12000);
    const memberRow = await waitFor(() => document.querySelector(`[data-member-user='${customerUserID}']`), "customer product membership");
    if ([...memberRow.querySelectorAll("button")].some((button) => button.textContent.trim() === "Remove")) {
      throw new Error("member removal should be inside the edit member modal");
    }
    memberRow.querySelector("button").click();
    const editRoot = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open] [name='role']") && rootNode?.querySelector("[data-member-action='delete']") ? rootNode : null;
    }, "edit member modal");
    setValue(editRoot.querySelector("[name='role']"), "viewer");
    editRoot.querySelector("form").requestSubmit();
    await waitFor(() => !modalRoot()?.querySelector("dialog")?.open, "edit member modal closed", 12000);
    await waitFor(() => document.querySelector(`[data-member-user='${customerUserID}']`)?.textContent.includes("Viewer"), "member role update");
    document.querySelector(`[data-member-user='${customerUserID}'] button`).click();
    const restoreRoot = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open] [name='role']") && rootNode?.querySelector("[data-member-action='delete']") ? rootNode : null;
    }, "restore member modal");
    setValue(restoreRoot.querySelector("[name='role']"), "customer");
    restoreRoot.querySelector("form").requestSubmit();
    await waitFor(() => !modalRoot()?.querySelector("dialog")?.open, "restore member modal closed", 12000);
    await waitFor(() => document.querySelector(`[data-member-user='${customerUserID}']`)?.textContent.includes("Customer"), "member role restored");
    document.querySelector(`[data-member-user='${customerUserID}'] button`).click();
    const deleteRoot = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open] [name='role']") && rootNode?.querySelector("[data-member-action='delete']") ? rootNode : null;
    }, "member delete modal");
    deleteRoot.querySelector("[data-member-action='delete']").click();
    await waitFor(() => deleteRoot.querySelector(".account-confirm.danger-zone")?.textContent.includes("Remove member"), "member delete confirmation");
    deleteRoot.querySelector(".account-confirm .ghost").click();
    await waitFor(() => !deleteRoot.querySelector(".account-confirm"), "member delete confirmation dismissed");
    document.querySelector("#modalHost").close();
    await waitFor(() => !modalRoot()?.querySelector("dialog")?.open, "member edit modal closed");
    document.querySelector("[data-product-section='webhooks']").click();
    await waitFor(() => window.location.pathname === `/products/${selectedProductID}/webhooks`, "product webhooks route");
    await waitFor(() => {
      return document.querySelector("[data-product-section='webhooks']")?.classList.contains("active") &&
        !document.querySelector("[data-product-panel='webhooks']")?.hidden;
    }, "product webhooks section");
    document.querySelector("#addWebhookButton").click();
    const webhookRoot = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open] [name='secret']") ? rootNode : null;
    }, "new product webhook modal");
    setValue(webhookRoot.querySelector("[name='name']"), "E2E webhook");
    setValue(webhookRoot.querySelector("[name='url']"), "https://hooks.example.test/e2e");
    setValue(webhookRoot.querySelector("[name='secret']"), "e2e-custom-secret");
    setValue(webhookRoot.querySelector("[name='events']"), "ticket.created");
    webhookRoot.querySelector("form").requestSubmit();
    await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open]") &&
        rootNode.textContent.includes("Webhook Secret Created") &&
        rootNode.querySelector("input[readonly]")?.value === "e2e-custom-secret";
    }, "webhook secret shown once");
    document.querySelector("#modalHost").close();
    await waitFor(() => !modalRoot()?.querySelector("dialog")?.open, "webhook secret modal closed");
    const webhookRow = await waitFor(() => {
      return [...document.querySelectorAll("#webhookList .admin-row")]
        .find((candidate) => candidate.textContent.includes("E2E webhook"));
    }, "created webhook row");
    webhookRow.querySelector("button").click();
    const editWebhookRoot = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open]") &&
        rootNode.textContent.includes("Stored secrets are not shown again") &&
        [...rootNode.querySelectorAll("button")].some((button) => button.textContent.includes("Rotate")) ? rootNode : null;
    }, "edit webhook modal");
    [...editWebhookRoot.querySelectorAll("button")]
      .find((button) => button.textContent.includes("Rotate"))
      .click();
    await waitFor(() => editWebhookRoot.textContent.includes("Rotate webhook secret?"), "webhook rotate confirmation");
    [...editWebhookRoot.querySelectorAll(".account-confirm button")]
      .find((button) => button.textContent.includes("Rotate"))
      .click();
    await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open]") &&
        rootNode.textContent.includes("Webhook Secret Rotated") &&
        rootNode.querySelector("input[readonly]")?.value &&
        rootNode.querySelector("input[readonly]").value !== "e2e-custom-secret";
    }, "rotated webhook secret shown once");
    document.querySelector("#modalHost").close();
    await waitFor(() => !modalRoot()?.querySelector("dialog")?.open, "webhook rotate modal closed");
  }, { productID, customerUsername: customer.username });
}

async function verifyProductRouteReload(cdp, productID) {
  await cdp.send("Page.reload", { ignoreCache: true });
  await waitForDocumentReady(cdp);
  await runInPage(cdp, async ({ productID: selectedProductID }) => {
    const { waitFor } = pageTools();
    await waitFor(() => {
      const view = document.querySelector("#productView");
      const title = document.querySelector("#productContextTitle")?.textContent.trim();
      return window.location.pathname === `/products/${selectedProductID}/webhooks` &&
        view &&
        !view.hidden &&
        title &&
        title !== "Product" &&
        document.querySelector("[data-product-section='webhooks']")?.classList.contains("active");
    }, "product route reload");
  }, { productID });
}

async function completeCustomerSetup(cdp, setupLink) {
  await cdp.send("Page.navigate", { url: setupLink });
  await waitForDocumentReady(cdp);
  await runInPage(cdp, async (input) => {
    const { setValue, waitFor } = pageTools();
    const form = await waitFor(() => {
      const candidate = document.querySelector("#accountLinkForm");
      return candidate && !candidate.hidden ? candidate : null;
    }, "account setup form");
    await waitFor(() => document.querySelector("#accountLinkUser")?.textContent.includes(input.email), "account setup identity");
    setValue(form.querySelector("[name='password']"), input.password);
    form.requestSubmit();
    await waitFor(() => {
      const app = document.querySelector("#appView");
      return app && !app.hidden && document.querySelector("#profileName")?.textContent.includes(input.displayName);
    }, "customer session after account setup", 12000);
    return true;
  }, customer);
}

async function createCustomerTicket(cdp) {
  return runInPage(cdp, async (input) => {
    const { isScrolledToBottom, modalRoot, openModalRoot, pasteFiles, setValue, tinyGifFile, waitFor } = pageTools();
    await waitFor(() => document.querySelector("#newTicketButton") && !document.querySelector("#newTicketButton").hidden, "new ticket button");
    document.querySelector("#newTicketButton").click();
    const root = await waitFor(() => {
      const candidate = modalRoot();
      const dialog = candidate?.querySelector("dialog[open]");
      const heading = candidate?.querySelector("h2")?.textContent || "";
      const title = candidate?.querySelector(".ticket-create-flow [name='title']");
      return dialog && heading.includes("New Ticket") && title ? candidate : null;
    }, "new ticket modal");
    const createModal = root.querySelector(".ticket-create-modal");
    if (document.querySelector("#ticketList .ticket-row.draft")) {
      throw new Error("ticket creation should use a modal, not a draft row");
    }
    const product = root.querySelector("[name='product_id']");
    if (!product.value) {
      const firstProduct = [...product.options].find((option) => option.value);
      setValue(product, firstProduct.value);
    }
    await waitFor(() => !root.querySelector("[name='priority']").disabled, "priority step enabled");
    setValue(root.querySelector("[name='priority']"), "high");
    await waitFor(() => !root.querySelector("[name='title']").disabled, "ticket detail step enabled");
    setValue(root.querySelector("[name='title']"), input.title);
    setValue(root.querySelector("[name='description']"), input.description);
    const createTransfer = new DataTransfer();
    createTransfer.items.add(new File(["dropped during ticket creation"], "e2e-create-drop.txt", { type: "text/plain" }));
    createTransfer.items.add(tinyGifFile("e2e-create-image.gif"));
    createModal.dispatchEvent(new DragEvent("dragenter", { bubbles: true, cancelable: true, dataTransfer: createTransfer }));
    await waitFor(() => createModal.classList.contains("ticket-create-drop-active"), "ticket creation drop highlight");
    await waitFor(() => {
      return getComputedStyle(createModal, "::before").backgroundColor !== "rgba(0, 0, 0, 0)" &&
        getComputedStyle(createModal, "::after").content.includes("Drop files to attach");
    }, "ticket creation drop label");
    createModal.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer: createTransfer }));
    await waitFor(() => {
      return [...root.querySelectorAll(".attachment-preview-chip")]
        .some((chip) => chip.textContent.includes("e2e-create-drop.txt"));
    }, "ticket creation dropped attachment chip");
    const createDropLink = [...root.querySelectorAll(".attachment-preview-chip .attachment-chip-name")]
      .find((link) => link.textContent.includes("e2e-create-drop.txt"));
    if (!createDropLink?.matches("a[download='e2e-create-drop.txt']") || !createDropLink.href.startsWith("blob:")) {
      throw new Error("ticket creation attachment chip should be a downloadable blob link");
    }
    const createImageChip = [...root.querySelectorAll(".attachment-preview-chip")]
      .find((chip) => chip.textContent.includes("e2e-create-image.gif"));
    if (!createImageChip?.querySelector(".attachment-chip-thumb[src^='blob:']")) {
      throw new Error("ticket creation image attachment should show a thumbnail");
    }
    pasteFiles(root.querySelector("[name='description']"), [
      new File(["pasted during ticket creation"], "e2e-create-paste.txt", { type: "text/plain" })
    ]);
    await waitFor(() => {
      return [...root.querySelectorAll(".attachment-preview-chip")]
        .some((chip) => chip.textContent.includes("e2e-create-paste.txt"));
    }, "ticket creation pasted attachment chip");
    const create = root.querySelector("footer .primary");
    await waitFor(() => !create.disabled, "enabled create ticket button");
    create.click();
    const confirmRoot = await waitFor(() => openModalRoot("Create this ticket?"), "stacked ticket create confirmation");
    if (confirmRoot === root) {
      throw new Error("ticket creation confirmation should open in a stacked modal");
    }
    const confirm = await waitFor(() => {
      const candidate = confirmRoot.querySelector("footer .primary");
      return candidate && !candidate.disabled ? candidate : null;
    }, "ticket create confirmation action");
    if (!confirmRoot.textContent.includes("Attachments") || !confirmRoot.textContent.includes("3")) {
      throw new Error("ticket create confirmation should include attachment count");
    }
    confirm.click();
    await waitFor(() => !modalRoot()?.querySelector("dialog[open]"), "new ticket modal closed", 12000);
    await waitFor(() => document.querySelector("#ticketList")?.textContent.includes(input.title), "created ticket in list", 12000);
    if (document.querySelector("#ticketList .ticket-row.draft")) {
      throw new Error("draft ticket row should not be present after creating a ticket");
    }
    const createdDetail = await waitFor(() => {
      const pane = document.querySelector("#ticketDetailPane");
      return pane?.textContent.includes(input.description) ? pane : null;
    }, "created ticket detail");
    const ownMessage = [...createdDetail.querySelectorAll(".message-row")]
      .find((candidate) => candidate.textContent.includes(input.description));
    if (!ownMessage?.classList.contains("from-current")) {
      throw new Error("customer opening message should be aligned as the current sender");
    }
    const ownAuthor = ownMessage.querySelector(".message-head-main strong")?.textContent?.trim();
    if (ownAuthor !== input.customerDisplayName) {
      throw new Error("customer opening message should display the account full name");
    }
    const ownAvatarRect = ownMessage.querySelector(".message-avatar").getBoundingClientRect();
    const ownConversation = createdDetail.querySelector(".conversation-stream");
    const ownConversationRect = ownConversation.getBoundingClientRect();
    const ownConversationStyle = getComputedStyle(ownConversation);
    const ownContentRight = ownConversationRect.left + ownConversation.clientWidth - parseFloat(ownConversationStyle.paddingRight || "0");
    if (ownConversationStyle.scrollBehavior !== "smooth") {
      throw new Error("conversation stream should use smooth programmatic scrolling");
    }
    await waitFor(() => isScrolledToBottom(ownConversation), "created ticket opens at latest message");
    if (ownAvatarRect.right < ownContentRight - 4) {
      throw new Error("current sender message should be visually aligned to the right");
    }
    const replyInput = createdDetail.querySelector(".comment-input");
    if (replyInput && getComputedStyle(replyInput).resize !== "none") {
      throw new Error("reply composer textarea should not be resizable");
    }
    await waitFor(() => /^#[A-Z][A-Z0-9]{1,15}-[1-9][0-9]*$/.test(window.location.hash), "ticket hash route after create");
    return decodeURIComponent(window.location.hash.slice(1));
  }, {
    ...ticket,
    customerDisplayName: customer.displayName
  });
}

async function verifyTicketHashRoute(cdp, ticketKey) {
  await cdp.send("Page.reload", { ignoreCache: true });
  await waitForDocumentReady(cdp);
  await runInPage(cdp, async ({ ticketKey: expectedKey, title }) => {
    const { waitFor } = pageTools();
    await waitFor(() => {
      const pane = document.querySelector("#ticketDetailPane");
      return window.location.pathname === "/tickets" &&
        decodeURIComponent(window.location.hash.slice(1)) === expectedKey &&
        pane?.textContent.includes(title) &&
        document.querySelector("#ticketList .ticket-row.active");
    }, "ticket hash route reload", 12000);
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await waitFor(() => window.location.hash === "" && document.querySelector("#ticketDetailPane")?.textContent.includes("No ticket selected"), "ticket hash clears on close");
  }, { ticketKey, title: ticket.title });
}

async function verifyFixedTicketLayout(cdp) {
  await runInPage(cdp, async () => {
    const { waitFor } = pageTools();
    await waitFor(() => document.body.classList.contains("app-mode"), "fixed app mode");
    await waitFor(() => document.querySelector(".conversation-stream"), "ticket conversation stream");
    const root = document.scrollingElement || document.documentElement;
    const extraScroll = root.scrollHeight - root.clientHeight;
    if (extraScroll > 2) {
      throw new Error(`app document should not scroll; overflow is ${extraScroll}px`);
    }
    const detailPane = document.querySelector("#ticketDetailPane");
    const conversation = document.querySelector(".conversation-stream");
    if (getComputedStyle(detailPane).overflowY !== "hidden") {
      throw new Error("ticket detail pane should keep overflow inside child regions");
    }
    const conversationOverflow = getComputedStyle(conversation).overflowY;
    if (!["auto", "scroll"].includes(conversationOverflow)) {
      throw new Error(`conversation stream should be internally scrollable; got ${conversationOverflow}`);
    }
    const composer = document.querySelector(".comment-form");
    const paneRect = detailPane.getBoundingClientRect();
    const composerRect = composer?.getBoundingClientRect();
    if (!composerRect || paneRect.bottom - composerRect.bottom > 32) {
      throw new Error("reply composer should stay anchored at the bottom of the ticket detail pane");
    }
    return true;
  });
}

async function verifySinglePaneTicketFlow(cdp, ticketKey, viewport, label) {
  await cdp.send("Emulation.setDeviceMetricsOverride", viewport);
  try {
    await runInPage(cdp, async (input) => {
      const { modalRoot, waitFor } = pageTools();
      await waitFor(() => {
        const list = document.querySelector(".ticket-list-pane");
        const detail = document.querySelector(".ticket-detail-pane");
        return list &&
          detail &&
          !document.querySelector("#ticketList .ticket-row.active") &&
          getComputedStyle(list).display !== "none" &&
          getComputedStyle(detail).display === "none";
      }, `${input.label} list default state`);

      const row = await waitFor(() => {
        return [...document.querySelectorAll("#ticketList .ticket-row")]
          .find((candidate) => candidate.textContent.includes(input.title));
      }, `${input.label} row`);
      row.click();

      const mobileHeader = await waitFor(() => {
        const list = document.querySelector(".ticket-list-pane");
        const detail = document.querySelector(".ticket-detail-pane");
        const header = document.querySelector(".ticket-mobile-header");
        return list &&
          detail &&
          header &&
          getComputedStyle(list).display === "none" &&
          getComputedStyle(detail).display !== "none" &&
          getComputedStyle(header).display !== "none" ? header : null;
      }, `${input.label} detail state`);
      if (!mobileHeader.textContent.includes(input.title) || mobileHeader.textContent.includes(input.ticketKey)) {
        throw new Error(`${input.label} header should show the selected ticket title without the ticket key`);
      }
      const detailPane = document.querySelector(".ticket-detail-pane");
      const detailRect = detailPane.getBoundingClientRect();
      const conversation = detailPane.querySelector(".conversation-stream");
      const composer = detailPane.querySelector(".comment-form");
      const conversationRect = conversation?.getBoundingClientRect();
      const composerRect = composer?.getBoundingClientRect();
      if (!conversationRect || !composerRect || detailRect.bottom - composerRect.bottom > 32) {
        throw new Error(`${input.label} reply composer should stay anchored at the bottom of the ticket detail pane`);
      }
      if (composerRect.top - conversationRect.bottom > 18 || conversationRect.height < 220) {
        throw new Error(`${input.label} conversation stream should expand before the reply composer`);
      }

      mobileHeader.querySelector(".mobile-ticket-info").click();
      const infoRoot = await waitFor(() => {
        const root = modalRoot();
        return root?.querySelector("dialog[open] h2")?.textContent.includes("Ticket Info") ? root : null;
      }, `${input.label} info sheet`);
      const sheet = infoRoot.querySelector(".ticket-info-sheet");
      if (!sheet?.textContent.includes("Product") || !sheet.textContent.includes("Ticket") || !sheet.textContent.includes(input.title)) {
        throw new Error(`${input.label} info sheet should contain ticket title and facts`);
      }
      infoRoot.querySelector("[value='cancel']").click();
      await waitFor(() => !modalRoot()?.querySelector("dialog[open]"), `${input.label} info sheet closed`);

      mobileHeader.querySelector(".mobile-ticket-back").click();
      await waitFor(() => {
        const list = document.querySelector(".ticket-list-pane");
        const detail = document.querySelector(".ticket-detail-pane");
        return !document.querySelector("#ticketList .ticket-row.active") &&
          getComputedStyle(list).display !== "none" &&
          getComputedStyle(detail).display === "none";
      }, `${input.label} back to ticket list`);

      document.querySelector("#ticketFilterButton").click();
      await waitFor(() => {
        const popover = document.querySelector("#ticketFilterPopover");
        if (!popover || popover.hidden) return false;
        const rect = popover.getBoundingClientRect();
        return getComputedStyle(popover).position === "fixed" &&
          rect.left >= -1 &&
          rect.right <= window.innerWidth + 1 &&
          rect.bottom <= window.innerHeight + 1;
      }, `${input.label} filter sheet`);
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));

      document.querySelector("#ticketSortButton").click();
      await waitFor(() => {
        const popover = document.querySelector("#ticketSortPopover");
        if (!popover || popover.hidden) return false;
        const rect = popover.getBoundingClientRect();
        return getComputedStyle(popover).position === "fixed" &&
          rect.left >= -1 &&
          rect.right <= window.innerWidth + 1 &&
          rect.bottom <= window.innerHeight + 1;
      }, `${input.label} sort sheet`);
      document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
      return true;
    }, { label, ticketKey, title: ticket.title });
  } finally {
    await cdp.send("Emulation.clearDeviceMetricsOverride");
  }
}

async function logout(cdp) {
  await runInPage(cdp, async () => {
    const { waitFor } = pageTools();
    document.querySelector("#profileButton").click();
    await waitFor(() => {
      const popover = document.querySelector("#profilePopover");
      return popover && !popover.hidden;
    }, "profile menu");
    document.querySelector("#logoutButton").click();
    await waitFor(() => {
      const form = document.querySelector("#loginForm");
      return form && !form.hidden;
    }, "login form after logout");
    return true;
  });
}

async function loginAsAdmin(cdp) {
  await runInPage(cdp, async (input) => {
    const { setValue, waitFor } = pageTools();
    const form = await waitFor(() => {
      const candidate = document.querySelector("#loginForm");
      return candidate && !candidate.hidden ? candidate : null;
    }, "login form");
    setValue(form.querySelector("[name='username']"), input.username);
    setValue(form.querySelector("[name='password']"), "wrong password");
    form.requestSubmit();
    await waitFor(() => {
      return form.contains(document.querySelector("#authError")) && !document.querySelector("#authError")?.hidden;
    }, "inline login error");
    setValue(form.querySelector("[name='username']"), input.username);
    setValue(form.querySelector("[name='password']"), input.password);
    form.requestSubmit();
    await waitFor(() => {
      const app = document.querySelector("#appView");
      return app && !app.hidden && document.querySelector("#profileName")?.textContent.includes(input.displayName);
    }, "admin session after login", 12000);
    return true;
  }, admin);
}

async function staffReplyAndResolve(cdp) {
  await runInPage(cdp, async (input) => {
    const { isScrolledToBottom, openModalRoot, pasteFiles, setValue, submitModal, tinyGifFile, waitFor } = pageTools();
    await waitFor(() => {
      const detailText = document.querySelector("#ticketDetailPane")?.textContent || "";
      return !document.querySelector("#ticketList .ticket-row.active") && detailText.includes("No ticket selected");
    }, "no ticket selected by default");
    let row = await waitFor(() => {
      return [...document.querySelectorAll("#ticketList .ticket-row")]
        .find((candidate) => candidate.textContent.includes(input.title));
    }, "ticket row for staff update", 12000);
    row = await waitFor(() => {
      return [...document.querySelectorAll("#ticketList .ticket-row")]
        .find((candidate) => candidate.textContent.includes(input.title) && candidate.classList.contains("unread"));
    }, "new customer ticket unread for staff");
    const unreadTitle = row.querySelector(".ticket-row-title");
    const unreadDot = row.querySelector(".ticket-unread-dot");
    if (!unreadDot || getComputedStyle(unreadDot).backgroundColor !== "rgb(217, 45, 32)") {
      throw new Error("unread ticket rows should use a red unread dot");
    }
    if (Number.parseInt(getComputedStyle(unreadTitle).fontWeight, 10) < 800 ||
      getComputedStyle(row).backgroundColor === "rgb(255, 255, 255)") {
      throw new Error("unread ticket rows should be visually stronger than read rows");
    }
    row.click();
    const detail = await waitFor(() => {
      const pane = document.querySelector("#ticketDetailPane");
      return pane?.querySelector("form [name='status']") ? pane : null;
    }, "ticket detail pane");
    await waitFor(() => {
      return !document.querySelector("#ticketList .ticket-row.active")?.classList.contains("unread");
    }, "ticket marked read after opening");
    const incomingMessage = [...detail.querySelectorAll(".message-row")]
      .find((candidate) => candidate.textContent.includes(input.description));
    if (!incomingMessage?.classList.contains("from-other")) {
      throw new Error("customer message should be aligned as incoming for staff");
    }
    const incomingAuthor = incomingMessage.querySelector(".message-head-main strong")?.textContent?.trim();
    if (incomingAuthor !== input.customerDisplayName) {
      throw new Error("incoming customer message should display the account full name");
    }
    const unreadDivider = detail.querySelector(".conversation-unread-divider");
    if (!unreadDivider?.textContent.includes("Unread")) {
      throw new Error("ticket conversation should mark where unread messages start");
    }
    if (unreadDivider.compareDocumentPosition(incomingMessage) !== Node.DOCUMENT_POSITION_FOLLOWING) {
      throw new Error("unread divider should appear before the first unread message");
    }
    const incomingAvatarRect = incomingMessage.querySelector(".message-avatar").getBoundingClientRect();
    const incomingConversation = detail.querySelector(".conversation-stream");
    const incomingConversationRect = incomingConversation.getBoundingClientRect();
    const incomingConversationStyle = getComputedStyle(incomingConversation);
    await waitFor(() => isScrolledToBottom(incomingConversation), "staff ticket opens at latest message");
    if (incomingAvatarRect.left > incomingConversationRect.left + parseFloat(incomingConversationStyle.paddingLeft || "0") + 4) {
      throw new Error("incoming message should be visually aligned to the left");
    }
    setValue(detail.querySelector("[name='status']"), "resolved");
    setValue(detail.querySelector("[name='body']"), input.reply);
    const composer = detail.querySelector(".comment-form");
    pasteFiles(detail.querySelector("[name='body']"), [
      new File(["pasted reply attachment"], "e2e-reply-paste.txt", { type: "text/plain" })
    ]);
    await waitFor(() => {
      return [...composer.querySelectorAll(".attachment-preview-chip")]
        .some((chip) => chip.textContent.includes("e2e-reply-paste.txt"));
    }, "reply pasted attachment chip");
    const replyPasteLink = [...composer.querySelectorAll(".attachment-preview-chip .attachment-chip-name")]
      .find((link) => link.textContent.includes("e2e-reply-paste.txt"));
    if (!replyPasteLink?.matches("a[download='e2e-reply-paste.txt']") || !replyPasteLink.href.startsWith("blob:")) {
      throw new Error("reply attachment chip should be a downloadable blob link");
    }
    const conversationDropTarget = detail.querySelector(".conversation-stream");
    const conversationPane = detail.querySelector(".ticket-main");
    const transfer = new DataTransfer();
    transfer.items.add(new File(["first attachment"], "e2e-first.txt", { type: "text/plain" }));
    transfer.items.add(new File(["second attachment"], "e2e-second.txt", { type: "text/plain" }));
    transfer.items.add(tinyGifFile("e2e-image.gif"));
    const cancelTransfer = new DataTransfer();
    cancelTransfer.items.add(new File(["cancelled attachment"], "e2e-cancelled.txt", { type: "text/plain" }));
    conversationDropTarget.dispatchEvent(new DragEvent("dragenter", { bubbles: true, cancelable: true, dataTransfer: cancelTransfer }));
    await waitFor(() => conversationPane.classList.contains("conversation-drop-active"), "conversation attachment cancelled drop highlight");
    conversationDropTarget.dispatchEvent(new DragEvent("dragleave", {
      bubbles: true,
      cancelable: true,
      dataTransfer: cancelTransfer,
      relatedTarget: document.body
    }));
    await waitFor(() => !conversationPane.classList.contains("conversation-drop-active"), "conversation attachment cancelled drop clears");
    conversationDropTarget.dispatchEvent(new DragEvent("dragenter", { bubbles: true, cancelable: true, dataTransfer: transfer }));
    await waitFor(() => conversationPane.classList.contains("conversation-drop-active"), "conversation attachment drop highlight");
    await waitFor(() => {
      return getComputedStyle(conversationPane, "::before").backgroundColor !== "rgba(0, 0, 0, 0)" &&
        getComputedStyle(conversationPane, "::after").content.includes("Drop files to attach");
    }, "conversation attachment drop label");
    conversationDropTarget.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer: transfer }));
    await waitFor(() => {
      const chipNames = [...composer.querySelectorAll(".attachment-preview-chip")].map((chip) => chip.textContent);
      return chipNames.some((name) => name.includes("e2e-first.txt")) &&
        chipNames.some((name) => name.includes("e2e-second.txt")) &&
        chipNames.some((name) => name.includes("e2e-reply-paste.txt")) &&
        chipNames.some((name) => name.includes("e2e-image.gif")) &&
        !conversationPane.classList.contains("conversation-drop-active");
    }, "reply attachment chips after conversation drop");
    const replyImageChip = [...composer.querySelectorAll(".attachment-preview-chip")]
      .find((chip) => chip.textContent.includes("e2e-image.gif"));
    if (!replyImageChip?.querySelector(".attachment-chip-thumb[src^='blob:']")) {
      throw new Error("reply image attachment should show a thumbnail");
    }
    const visibility = detail.querySelector("[name='visibility']");
    if (visibility) setValue(visibility, "public");
    const submit = detail.querySelector("[data-comment-send]");
    await waitFor(() => !submit.disabled, "enabled send reply button");
    submit.click();
    await submitModal("Send this reply?");
    await waitFor(() => {
      const detailText = document.querySelector("#ticketDetailPane")?.textContent || "";
      const stillListed = [...document.querySelectorAll("#ticketList .ticket-row")]
        .some((candidate) => candidate.textContent.includes(input.title));
      return detailText.includes(input.reply) || !stillListed;
    }, "saved ticket update", 12000);
    const staffReply = [...detail.querySelectorAll(".message-row")]
      .find((candidate) => candidate.textContent.includes(input.reply));
    if (staffReply && !staffReply.classList.contains("from-current")) {
      throw new Error("staff reply should be aligned as the current sender");
    }
    if (staffReply) {
      const staffAuthor = staffReply.querySelector(".message-head-main strong")?.textContent?.trim();
      if (staffAuthor !== input.adminDisplayName) {
        throw new Error("staff reply should display the account full name");
      }
      const staffAvatarRect = staffReply.querySelector(".message-avatar").getBoundingClientRect();
      const staffConversation = detail.querySelector(".conversation-stream");
      const staffConversationRect = staffConversation.getBoundingClientRect();
      const staffConversationStyle = getComputedStyle(staffConversation);
      const staffContentRight = staffConversationRect.left + staffConversation.clientWidth - parseFloat(staffConversationStyle.paddingRight || "0");
      if (staffAvatarRect.right < staffContentRight - 4) {
        throw new Error("staff reply should be visually aligned to the right");
      }
    }
    await waitFor(() => {
      const preview = document.querySelector(".attachment-image-preview");
      return preview?.getAttribute("src")?.includes("?preview=1");
    }, "image attachment inline preview");
    const deleteButton = detail.querySelector("[data-delete-ticket]");
    if (!deleteButton || !deleteButton.classList.contains("danger")) {
      throw new Error("admin ticket detail should expose a danger delete action");
    }
    deleteButton.click();
    const deleteRoot = await waitFor(() => openModalRoot("Delete this ticket?"), "ticket delete confirmation");
    if (!deleteRoot.querySelector("footer .danger") || !deleteRoot.textContent.includes(input.title)) {
      throw new Error("ticket deletion confirmation should identify the ticket and use a danger action");
    }
    deleteRoot.querySelector("footer .ghost").click();
    await waitFor(() => !openModalRoot("Delete this ticket?"), "ticket delete confirmation dismissed");
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    await waitFor(() => {
      const detailText = document.querySelector("#ticketDetailPane")?.textContent || "";
      return !document.querySelector("#ticketList .ticket-row.active") && detailText.includes("No ticket selected");
    }, "ticket closed with escape");
    return true;
  }, {
    ...ticket,
    adminDisplayName: admin.displayName,
    customerDisplayName: customer.displayName
  });
}

async function verifyEmailOutbox(cdp) {
  await runInPage(cdp, async (input) => {
    const { modalRoot, waitFor } = pageTools();
    document.querySelector("#adminTab").click();
    await waitFor(() => {
      const view = document.querySelector("#adminView");
      return view && !view.hidden;
    }, "admin view for email outbox");
    document.querySelector("[data-admin-section='email']").click();
    const row = await waitFor(() => {
      return [...document.querySelectorAll("#emailList .email-row")].find((candidate) => {
        const text = candidate.textContent;
        return text.includes("ticket.commented") && text.includes(input.customerEmail);
      });
    }, "customer ticket.commented notification", 12000);
    row.click();
    await waitFor(() => {
      const root = modalRoot();
      return root?.querySelector("dialog[open]") && root.textContent.includes(input.reply);
    }, "email notification body includes public reply");
    document.querySelector("#modalHost").close();
    return true;
  }, { customerEmail: customer.email, reply: ticket.reply });
}

async function verifyAuditLog(cdp) {
  const actions = await runInPage(cdp, async () => {
    const response = await fetch("/api/audit-events", { credentials: "same-origin" });
    if (!response.ok) throw new Error(`audit API returned ${response.status}`);
    const payload = await response.json();
    return (payload.events || []).map((event) => event.action);
  });
  for (const action of ["setup.completed", "user.created", "product_member.upserted"]) {
    if (!actions.includes(action)) {
      throw new Error(`audit action ${action} missing from ${JSON.stringify(actions)}`);
    }
  }
}

function pageTools() {
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const waitFor = async (predicate, description, timeoutMs = 8000) => {
    const started = Date.now();
    let lastError = null;
    while (Date.now() - started < timeoutMs) {
      try {
        const value = predicate();
        if (value) return value;
      } catch (error) {
        lastError = error;
      }
      await sleep(50);
    }
    const suffix = lastError ? ` (${lastError.message})` : "";
    throw new Error(`Timed out waiting for ${description}${suffix}`);
  };
  const setValue = (control, value) => {
    if (!control) throw new Error(`Missing form control for value ${value}`);
    control.focus();
    control.value = value;
    control.dispatchEvent(new Event("input", { bubbles: true }));
    control.dispatchEvent(new Event("change", { bubbles: true }));
  };
  const pasteFiles = (target, files) => {
    if (!target) throw new Error("Missing paste target");
    const transfer = new DataTransfer();
    for (const file of files) transfer.items.add(file);
    const event = new Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(event, "clipboardData", { value: transfer });
    target.dispatchEvent(event);
  };
  const tinyGifFile = (name) => new File([new Uint8Array([
    0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00,
    0x01, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00,
    0xff, 0xff, 0xff, 0x2c, 0x00, 0x00, 0x00, 0x00,
    0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x44,
    0x01, 0x00, 0x3b
  ])], name, { type: "image/gif" });
  const modalRoot = () => document.querySelector("#modalHost")?.shadowRoot || null;
  const modalRoots = () => [...document.querySelectorAll("pappice-modal")]
    .map((modal) => modal.shadowRoot)
    .filter(Boolean);
  const openModalRoot = (title = "") => modalRoots().find((root) => {
    const dialog = root.querySelector("dialog[open]");
    const heading = root.querySelector("h2")?.textContent || "";
    return dialog && (!title || heading.includes(title));
  }) || null;
  const submitModal = async (title) => {
    const root = await waitFor(() => {
      const candidate = modalRoot();
      const dialog = candidate?.querySelector("dialog[open]");
      const heading = candidate?.querySelector("h2")?.textContent || "";
      return dialog && (!title || heading.includes(title)) ? candidate : null;
    }, title ? `${title} modal` : "modal");
    root.querySelector("footer .primary").click();
    await waitFor(() => !modalRoot()?.querySelector("dialog[open]"), "modal closed", 12000);
    return true;
  };
  const isScrolledToBottom = (element) => {
    if (!element) return false;
    return Math.abs(element.scrollHeight - element.clientHeight - element.scrollTop) <= 2;
  };
  return { isScrolledToBottom, modalRoot, openModalRoot, pasteFiles, setValue, submitModal, tinyGifFile, waitFor };
}

async function runInPage(cdp, fn, ...args) {
  const expression = `(() => {
    const pageTools = ${pageTools.toString()};
    return (${fn.toString()})(...${JSON.stringify(args)});
  })()`;
  const response = await cdp.send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
    userGesture: true
  });
  if (response.exceptionDetails) {
    throw new Error(formatException(response.exceptionDetails));
  }
  return response.result?.value;
}

function formatException(details) {
  const lines = [details.exception?.description || details.text || "browser evaluation failed"];
  for (const frame of details.stackTrace?.callFrames || []) {
    lines.push(`  at ${frame.functionName || "<anonymous>"}:${frame.lineNumber + 1}:${frame.columnNumber + 1}`);
  }
  return lines.join("\n");
}

async function waitForDocumentReady(cdp) {
  await runInPage(cdp, async () => {
    const { waitFor } = pageTools();
    await waitFor(() => document.readyState === "complete", "document ready", 12000);
    return true;
  });
}

class CDPClient {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 1;
    this.pending = new Map();
    socket.addEventListener("message", (event) => this.handleMessage(event.data));
    socket.addEventListener("close", () => {
      for (const { reject, timer } of this.pending.values()) {
        clearTimeout(timer);
        reject(new Error("CDP websocket closed"));
      }
      this.pending.clear();
    });
  }

  send(method, params = {}) {
    const id = this.nextID++;
    const payload = JSON.stringify({ id, method, params });
    return new Promise((resolvePromise, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`CDP ${method} timed out`));
      }, 30000);
      this.pending.set(id, { method, resolve: resolvePromise, reject, timer });
      this.socket.send(payload);
    });
  }

  async close() {
    if (this.socket.readyState === 3) return;
    await new Promise((resolvePromise) => {
      let settled = false;
      const settle = () => {
        if (settled) return;
        settled = true;
        resolvePromise();
      };
      this.socket.addEventListener("close", settle, { once: true });
      this.socket.close();
      setTimeout(settle, 1000).unref();
    });
  }

  handleMessage(raw) {
    const message = JSON.parse(String(raw));
    if (!message.id) return;
    const pending = this.pending.get(message.id);
    if (!pending) return;
    clearTimeout(pending.timer);
    this.pending.delete(message.id);
    if (message.error) {
      pending.reject(new Error(`CDP ${pending.method} failed: ${message.error.message}`));
      return;
    }
    pending.resolve(message.result || {});
  }
}

async function connectToPage(port) {
  const deadline = Date.now() + 12000;
  let target = null;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`http://127.0.0.1:${port}/json/list`);
      const targets = await response.json();
      target = targets.find((candidate) => candidate.type === "page" && candidate.webSocketDebuggerUrl);
      if (target) break;
    } catch {
      // Chromium is still starting.
    }
    await sleep(100);
  }
  if (!target) throw new Error("Chromium DevTools page target was not available");
  const socket = await connectWebSocket(target.webSocketDebuggerUrl);
  return new CDPClient(socket);
}

async function connectWebSocket(url) {
  return new Promise((resolvePromise, reject) => {
    const socket = new WebSocket(url);
    const timer = setTimeout(() => {
      socket.close();
      reject(new Error("Timed out connecting to Chromium DevTools websocket"));
    }, 10000);
    socket.addEventListener("open", () => {
      clearTimeout(timer);
      resolvePromise(socket);
    }, { once: true });
    socket.addEventListener("error", (event) => {
      clearTimeout(timer);
      reject(new Error(`DevTools websocket error: ${event.message || "unknown error"}`));
    }, { once: true });
  });
}

function startChromium(port, appURL, userDataDir) {
  return spawnProcess(chromiumPath, [
    "--headless=new",
    `--remote-debugging-port=${port}`,
    "--remote-allow-origins=*",
    `--user-data-dir=${userDataDir}`,
    "--ignore-certificate-errors",
    "--no-first-run",
    "--disable-gpu",
    "--disable-dev-shm-usage",
    "--no-sandbox",
    appURL
  ], {
    cwd: repoRoot,
    label: "chromium"
  });
}

async function buildApp(binaryPath) {
  await runCommand("go", ["build", "-o", binaryPath, "./cmd/pappice"], { cwd: repoRoot });
}

function startApp({ appPort, appURL, binaryPath, certPath, dbPath, keyPath, smtpPort }) {
  const env = {
    ...process.env,
    PAPPICE_EMAIL_NOTIFICATIONS: "true",
    PAPPICE_PUBLIC_URL: appURL,
    PAPPICE_SMTP_FROM: "no-reply@example.test",
    PAPPICE_SMTP_HOST: "127.0.0.1",
    PAPPICE_SMTP_PASSWORD: "",
    PAPPICE_SMTP_PORT: String(smtpPort),
    PAPPICE_SMTP_TLS_MODE: "none",
    PAPPICE_SMTP_USER: ""
  };
  return spawnProcess(binaryPath, [
    "serve",
    "-addr", `127.0.0.1:${appPort}`,
    "-db", dbPath,
    "-tls-cert", certPath,
    "-tls-key", keyPath,
    "-public-url", appURL,
    "-email-notifications",
    "-smtp-host", "127.0.0.1",
    "-smtp-port", String(smtpPort),
    "-smtp-from", "no-reply@example.test",
    "-smtp-tls-mode", "none",
    "-email-batch-delay", "1h",
    "-session-ttl", "2h"
  ], {
    cwd: repoRoot,
    env,
    label: "pappice"
  });
}

function spawnProcess(command, args, { cwd, env = process.env, label }) {
  const child = spawn(command, args, {
    cwd,
    env,
    stdio: ["ignore", "pipe", "pipe"]
  });
  const output = [];
  child.stdout.on("data", (chunk) => captureOutput(output, label, chunk));
  child.stderr.on("data", (chunk) => captureOutput(output, label, chunk));
  child.outputText = () => Buffer.concat(output).toString("utf8");
  child.on("error", (error) => {
    output.push(Buffer.from(`${label}: ${error.message}\n`));
  });
  return child;
}

function captureOutput(output, label, chunk) {
  output.push(Buffer.from(chunk));
  if (process.env.PAPPICE_E2E_VERBOSE) {
    const text = chunk.toString("utf8").replace(/\n$/, "");
    for (const line of text.split("\n")) {
      if (line) console.error(`[${label}] ${line}`);
    }
  }
}

async function waitForHTTPS(url, child, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  let lastError = null;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`app exited before becoming ready\n${child.outputText()}`);
    }
    try {
      const status = await httpsStatus(url);
      if (status >= 200 && status < 500) return;
    } catch (error) {
      lastError = error;
    }
    await sleep(150);
  }
  throw new Error(`app did not become ready: ${lastError?.message || "timeout"}\n${child.outputText()}`);
}

async function httpsStatus(url) {
  return new Promise((resolvePromise, reject) => {
    const request = https.get(url, { rejectUnauthorized: false, timeout: 1500 }, (response) => {
      response.resume();
      response.on("end", () => resolvePromise(response.statusCode || 0));
    });
    request.on("timeout", () => {
      request.destroy(new Error("request timed out"));
    });
    request.on("error", reject);
  });
}

async function generateCertificate(certPath, keyPath) {
  await runCommand("openssl", [
    "req",
    "-x509",
    "-newkey", "rsa:2048",
    "-nodes",
    "-keyout", keyPath,
    "-out", certPath,
    "-days", "1",
    "-subj", "/CN=127.0.0.1",
    "-addext", "subjectAltName=IP:127.0.0.1,DNS:localhost"
  ], { cwd: repoRoot });
}

async function runCommand(command, args, options = {}) {
  const child = spawn(command, args, {
    ...options,
    stdio: ["ignore", "pipe", "pipe"]
  });
  const output = [];
  child.stdout.on("data", (chunk) => output.push(Buffer.from(chunk)));
  child.stderr.on("data", (chunk) => output.push(Buffer.from(chunk)));
  const code = await new Promise((resolvePromise, reject) => {
    child.on("error", reject);
    child.on("exit", (exitCode) => resolvePromise(exitCode));
  });
  if (code !== 0) {
    throw new Error(`${command} exited with ${code}\n${Buffer.concat(output).toString("utf8")}`);
  }
}

async function startFakeSMTP() {
  const messages = [];
  const sockets = new Set();
  const server = net.createServer((socket) => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    socket.setEncoding("utf8");
    socket.write("220 pappice-e2e\r\n");
    let buffer = "";
    let dataMode = false;
    let dataLines = [];
    let message = { from: "", to: [], data: "" };

    socket.on("data", (chunk) => {
      buffer += chunk;
      while (buffer.includes("\n")) {
        const index = buffer.indexOf("\n");
        const line = buffer.slice(0, index).replace(/\r$/, "");
        buffer = buffer.slice(index + 1);
        if (dataMode) {
          if (line === ".") {
            message.data = dataLines.join("\r\n");
            messages.push(message);
            message = { from: "", to: [], data: "" };
            dataLines = [];
            dataMode = false;
            socket.write("250 OK\r\n");
          } else {
            dataLines.push(line);
          }
          continue;
        }

        const upper = line.toUpperCase();
        if (upper.startsWith("EHLO") || upper.startsWith("HELO")) {
          socket.write("250-localhost\r\n250 OK\r\n");
        } else if (upper.startsWith("AUTH")) {
          socket.write("235 Authentication successful\r\n");
        } else if (upper.startsWith("MAIL FROM:")) {
          message.from = line.slice("MAIL FROM:".length).trim();
          socket.write("250 OK\r\n");
        } else if (upper.startsWith("RCPT TO:")) {
          message.to.push(line.slice("RCPT TO:".length).trim());
          socket.write("250 OK\r\n");
        } else if (upper === "DATA") {
          dataMode = true;
          socket.write("354 End data with <CR><LF>.<CR><LF>\r\n");
        } else if (upper === "RSET") {
          message = { from: "", to: [], data: "" };
          dataLines = [];
          dataMode = false;
          socket.write("250 OK\r\n");
        } else if (upper === "NOOP") {
          socket.write("250 OK\r\n");
        } else if (upper === "QUIT") {
          socket.write("221 Bye\r\n");
          socket.end();
        } else {
          socket.write("250 OK\r\n");
        }
      }
    });
  });
  await new Promise((resolvePromise, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      server.off("error", reject);
      resolvePromise();
    });
  });
  return {
    messages,
    port: server.address().port,
    close: () => new Promise((resolvePromise) => {
      let settled = false;
      const settle = () => {
        if (settled) return;
        settled = true;
        resolvePromise();
      };
      for (const socket of sockets) socket.destroy();
      server.close(settle);
      setTimeout(settle, 1000).unref();
    })
  };
}

async function freePort() {
  const server = net.createServer();
  await new Promise((resolvePromise, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolvePromise);
  });
  const port = server.address().port;
  await new Promise((resolvePromise) => server.close(resolvePromise));
  return port;
}

async function cleanup() {
  if (page) {
    try {
      await page.close();
    } catch {
      // Ignore cleanup failures.
    }
    page = null;
  }
  await stopProcess(chromeProcess);
  chromeProcess = null;
  await stopProcess(appProcess);
  appProcess = null;
  if (smtpServer) {
    try {
      await smtpServer.close();
    } catch {
      // Ignore cleanup failures.
    }
    smtpServer = null;
  }
  if (tempDir && !process.env.PAPPICE_E2E_KEEP_TMP) {
    await rm(tempDir, { force: true, recursive: true });
  }
}

async function stopProcess(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGTERM");
  const stopped = await waitForExit(child, 2500);
  if (!stopped) {
    child.kill("SIGKILL");
    await waitForExit(child, 2500);
  }
}

async function waitForExit(child, timeoutMs) {
  return new Promise((resolvePromise) => {
    const timer = setTimeout(() => resolvePromise(false), timeoutMs);
    child.once("exit", () => {
      clearTimeout(timer);
      resolvePromise(true);
    });
  });
}

function sleep(ms) {
  return new Promise((resolvePromise) => setTimeout(resolvePromise, ms));
}
