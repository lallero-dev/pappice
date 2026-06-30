import { admin, customer, ticket } from "./fixtures.mjs";
import { pressKey, runInPage, waitForDocumentReady } from "../tools/browser-page.mjs";

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
    const ownTime = ownMessage.querySelector(".comment-time")?.textContent?.trim() || "";
    if (!ownTime || ownTime.startsWith("opened ") || /^(now|\d+[mhd])$/.test(ownTime)) {
      throw new Error(`conversation timestamps should show full date/time, got ${JSON.stringify(ownTime)}`);
    }
    const ownLink = ownMessage.querySelector(".message-link");
    if (!ownLink || ownLink.textContent !== input.url || ownLink.getAttribute("href") !== input.url) {
      throw new Error("conversation URLs should be linkified without consuming trailing punctuation");
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
    const imagePreviewButton = document.querySelector(".attachment-image-link");
    if (!imagePreviewButton) {
      throw new Error("image attachment should expose a preview button");
    }
    imagePreviewButton.click();
    await waitFor(() => document.querySelector(".image-preview-modal[open]"), "image preview modal");
    return true;
  }, {
    ...ticket,
    adminDisplayName: admin.displayName,
    customerDisplayName: customer.displayName
  });

  await pressKey(cdp, "Escape");

  await runInPage(cdp, async (input) => {
    const { openModalRoot, waitFor } = pageTools();
    await waitFor(() => !document.querySelector(".image-preview-modal[open]"), "image preview closed with escape");
    const detail = await waitFor(() => {
      const pane = document.querySelector("#ticketDetailPane");
      return document.querySelector("#ticketList .ticket-row.active") &&
        pane?.textContent.includes(input.reply) ? pane : null;
    }, "ticket remains open after image preview escape");
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

export {
  createCustomerTicket,
  verifyTicketHashRoute,
  verifyFixedTicketLayout,
  verifySinglePaneTicketFlow,
  staffReplyAndResolve
};
