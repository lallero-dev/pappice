import { logout } from "./accounts.mjs";
import { runInPage, waitForDocumentReady } from "../tools/browser-page.mjs";

export async function verifyInternalContributor(page, productID) {
  const account = { displayName: "Internal Contributor", email: "contributor@example.test", password: "correct horse battery" };
  const created = await runInPage(page, async ({ account, productID }) => {
    const { request } = await import("/static/api.js");
    const user = await request("/api/users", {
      method: "POST",
      body: JSON.stringify({ display_name: account.displayName, email: account.email, password: account.password, role: "staff" })
    });
    const ticket = await request("/api/tickets", {
      method: "POST",
      body: JSON.stringify({ product_id: Number(productID), title: "Contributor investigation" })
    });
    await request(`/api/tickets/${ticket.id}`, {
      method: "PATCH",
      body: JSON.stringify({ status: "closed", comment: { body: "Existing private investigation", visibility: "internal" } })
    });
    return { userID: user.id, ticketID: ticket.id, key: ticket.key, origin: location.origin };
  }, { account, productID });

  await page.send("Page.navigate", { url: `${created.origin}/products/${productID}/members` });
  await waitForDocumentReady(page);
  await runInPage(page, async ({ userID }) => {
    const { modalRoot, setValue, waitFor } = pageTools();
    const add = await waitFor(() => {
      const button = document.querySelector("#addMemberButton");
      return button && !document.querySelector("[data-product-panel='members']")?.hidden ? button : null;
    }, "product members");
    add.click();
    const root = await waitFor(() => modalRoot()?.querySelector("dialog[open] [name='user_id']") ? modalRoot() : null, "add member dialog");
    setValue(root.querySelector("[name='user_id']"), String(userID));
    const role = root.querySelector("[name='role']");
    const option = [...role.options].find((item) => item.value === "internal_contributor");
    if (!option || option.textContent !== "Internal Contributor") throw new Error("internal contributor role is missing");
    setValue(role, "internal_contributor");
    if (!root.textContent.includes("Cannot send public replies, create tickets, or edit ticket details.")) {
      throw new Error("role selector does not explain the internal contributor restrictions");
    }
    root.querySelector("form").requestSubmit();
    await waitFor(() => !modalRoot()?.querySelector("dialog[open]"), "member saved");
    const row = await waitFor(() => {
      const row = document.querySelector(`[data-member-user='${userID}']`);
      return row?.textContent.includes("Internal Contributor") ? row : null;
    }, "contributor membership");
    row.querySelector("button").click();
    await waitFor(() => modalRoot()?.querySelector("dialog[open] [name='role']")?.value === "internal_contributor", "saved role in edit dialog");
    document.querySelector("#modalHost").close();
  }, created);

  await logout(page);
  await runInPage(page, async (account) => {
    const { setValue, waitFor } = pageTools();
    const form = document.querySelector("#loginForm");
    setValue(form.querySelector("[name='email']"), account.email);
    setValue(form.querySelector("[name='password']"), account.password);
    form.requestSubmit();
    await waitFor(() => !document.querySelector("#appView")?.hidden && document.querySelector("#profileName")?.textContent.includes(account.displayName), "contributor login");
  }, account);
  await page.send("Page.navigate", { url: `${created.origin}/tickets#${created.key}` });
  await waitForDocumentReady(page);
  await runInPage(page, async ({ ticketID }) => {
    const { openModalRoot, pasteFiles, setValue, waitFor } = pageTools();
    const { request } = await import("/static/api.js");
    const detail = await waitFor(() => {
      const pane = document.querySelector("#ticketDetailPane");
      return pane?.textContent.includes("Existing private investigation") && pane.querySelector(".comment-form") ? pane : null;
    }, "contributor ticket detail");
    if (!document.querySelector("#newTicketButton").hidden || !document.querySelector("#productTab").hidden) {
      throw new Error("contributor has ticket creation or product management controls");
    }
    if (detail.querySelector("[name='title'], [name='priority'], [name='status'], [name='assignee_user_id']")) {
      throw new Error("contributor has editable ticket fields");
    }
    const composer = detail.querySelector(".comment-form");
    if (composer.querySelector("select[name='visibility']") || composer.querySelector("[name='visibility']")?.value !== "internal") {
      throw new Error("contributor composer must be fixed to internal notes");
    }
    if (!composer.textContent.includes("Only staff can see this") || composer.querySelector("[data-comment-send]")?.textContent !== "Add note") {
      throw new Error("internal note audience or action is unclear");
    }
    setValue(composer.querySelector("[name='body']"), "Contributor suggestion");
    pasteFiles(composer.querySelector("[name='body']"), [new File(["Internal attachment"], "investigation.txt", { type: "text/plain" })]);
    composer.querySelector("[data-comment-send]").click();
    const confirm = await waitFor(() => openModalRoot("Save this internal note?"), "internal note confirmation");
    confirm.querySelector("footer .primary").click();
    await waitFor(() => document.querySelector("#ticketDetailPane .conversation-stream")?.textContent.includes("Contributor suggestion"), "saved internal note");
    const saved = await request(`/api/tickets/${ticketID}`);
    const note = saved.comments.find((comment) => comment.body === "Contributor suggestion");
    if (saved.status !== "closed" || note?.visibility !== "internal" || note.author !== "Internal Contributor" || note.attachments?.length !== 1) {
      throw new Error("contributor note changed status, visibility, author, or attachments");
    }
    // A note consisting only of an attachment must also stay internal.
    const next = document.querySelector("#ticketDetailPane .comment-form");
    pasteFiles(next.querySelector("[name='body']"), [new File(["More context"], "context.txt", { type: "text/plain" })]);
    next.querySelector("[data-comment-send]").click();
    const attachmentConfirm = await waitFor(() => openModalRoot("Save this internal note?"), "attachment-only note confirmation");
    attachmentConfirm.querySelector("footer .primary").click();
    await waitFor(() => document.querySelector("#ticketDetailPane .conversation-stream")?.textContent.includes("context.txt"), "attachment-only internal note");
    const final = await request(`/api/tickets/${ticketID}`);
    if (final.comments.length !== 3 || final.comments[2].visibility !== "internal" || final.comments[2].body !== "") {
      throw new Error("attachment-only note did not stay internal");
    }
  }, created);
}
