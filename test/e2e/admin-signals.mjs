import { customer, ticket } from "./fixtures.mjs";
import { runInPage } from "../tools/browser-page.mjs";

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

export {
  verifyEmailOutbox,
  verifyAuditLog
};
