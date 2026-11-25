import { runInPage } from "../tools/browser-page.mjs";

export async function verifyTicketUpdates(cdp, productID) {
  await runInPage(cdp, async (productID) => {
    const { setValue, submitModal, waitFor } = pageTools();
    const { request } = await import("/static/api.js");
    const { platform } = await import("/static/platform.js");
    const { state } = await import("/static/state.js");
    const { applyTicketRouteSelection, loadTickets, selectedTicket, setSelectedTicket,
      startTicketRefreshLoop, stopTicketRefreshLoop } = await import("/static/tickets.js");
    const originalFetch = platform.fetch;
    const failures = [];
    let releaseResponse;
    document.querySelector("#ticketsTab").click();
    stopTicketRefreshLoop();
    try {
      const ticket = await request("/api/tickets", {
        method: "POST",
        body: JSON.stringify({ product_id: Number(productID), title: "Concurrent ticket updates" })
      });
      const path = `/api/tickets/${ticket.id}`;
      await loadTickets({ renderDetail: false });
      platform.fetch = async (url, options) => {
        const response = await originalFetch(url, options);
        if (url === path) await new Promise((resolve) => { releaseResponse = resolve; });
        return response;
      };
      const opening = applyTicketRouteSelection(ticket.key);
      await waitFor(() => releaseResponse, "pending ticket selection");
      await applyTicketRouteSelection("");
      releaseResponse();
      await opening;
      if (selectedTicket()) failures.push("A delayed selection reopened a ticket after leaving its route");
      platform.fetch = originalFetch;

      for (const phase of ["refresh", "read"]) {
        await applyTicketRouteSelection(ticket.key);
        releaseResponse = null;
        let resumed = false;
        platform.fetch = async (url, options = {}) => {
          const response = await originalFetch(url, options);
          const delayed = phase === "read"
            ? url === `${path}/read`
            : url === path && (options.method || "GET") === "GET";
          if (delayed) {
            await new Promise((resolve) => { releaseResponse = resolve; });
            resumed = true;
          }
          return response;
        };
        if (phase === "read") {
          setSelectedTicket({ ...selectedTicket(), has_unread: true }, { updateRoute: false });
          await loadTickets();
        } else {
          startTicketRefreshLoop({ immediate: true });
        }
        await waitFor(() => releaseResponse, `pending ${phase} response`);
        stopTicketRefreshLoop();
        const detail = document.querySelector("#ticketDetailPane");
        const reply = `Reply saved during ${phase}`;
        setValue(detail.querySelector("[name='body']"), reply);
        detail.querySelector("[data-comment-send]").click();
        await submitModal("Send this reply?");
        await waitFor(() => selectedTicket()?.comments?.some((comment) => comment.body === reply), "saved reply");
        releaseResponse();
        await waitFor(() => resumed, "delayed response resumed");
        await new Promise((resolve) => setTimeout(resolve, 0));
        if (!selectedTicket()?.comments?.some((comment) => comment.body === reply) || !detail.textContent.includes(reply)) {
          failures.push(`A delayed ${phase} response overwrote the saved reply`);
        }
        platform.fetch = originalFetch;
        stopTicketRefreshLoop();
      }

      await applyTicketRouteSelection(ticket.key);
      await request(path, { method: "PATCH", body: JSON.stringify({ priority: "urgent" }) });
      startTicketRefreshLoop({ immediate: true });
      await waitFor(() => selectedTicket()?.priority === "urgent", "remote priority update");
      stopTicketRefreshLoop();
      const assignee = document.querySelector("#ticketDetailPane [name='assignee_user_id']");
      setValue(assignee, String(state.user.id));
      await waitFor(() => selectedTicket()?.assignee_user_id === state.user.id, "assignee autosave");
      const saved = await request(path);
      if (saved.priority !== "urgent") failures.push("Editing the assignee reverted a remote priority change");
      if (failures.length) throw new Error(failures.join("\n"));
    } finally {
      releaseResponse?.();
      platform.fetch = originalFetch;
      startTicketRefreshLoop();
    }
  }, productID);
}
