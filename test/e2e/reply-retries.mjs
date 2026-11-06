import { runInPage } from "../tools/browser-page.mjs";

export async function verifyReplyRetries(cdp, productID) {
  await runInPage(cdp, async (productID) => {
    const { setValue, submitModal, waitFor } = pageTools();
    const { request } = await import("/static/api.js");
    const { platform } = await import("/static/platform.js");
    const { applyTicketRouteSelection, closeSelectedTicket, startTicketRefreshLoop, stopTicketRefreshLoop } = await import("/static/tickets.js");
    const originalFetch = platform.fetch;
    document.querySelector("#ticketsTab").click();
    stopTicketRefreshLoop();
    try {
      for (const withAttachment of [false, true]) {
        const ticket = await request("/api/tickets", {
          method: "POST",
          body: JSON.stringify({ product_id: Number(productID), title: `Reply retry ${withAttachment ? "with attachment" : "JSON"}` })
        });
        await applyTicketRouteSelection(ticket.key);
        const path = `/api/tickets/${ticket.id}`;
        const reply = "A reply whose response was lost";
        const posts = [];
        let loseResponse = true;
        let failRefresh = false;
        let detailReads = 0;
        let releaseResponse;
        platform.fetch = async (url, options = {}) => {
          if (url === path && (options.method || "GET") === "GET") detailReads++;
          if (failRefresh && url.startsWith("/api/tickets?")) {
            failRefresh = false;
            throw new Error("Simulated list refresh failure");
          }
          const response = await originalFetch(url, options);
          if (url === `${path}/comments` && options.method === "POST") {
            if (!response.ok) throw new Error(`Reply failed: ${response.status}`);
            posts.push({
              key: new Headers(options.headers).get("Idempotency-Key"),
              replayed: response.headers.get("Idempotency-Replayed") === "true"
            });
            if (loseResponse) {
              loseResponse = false;
              await response.text();
              throw new Error("Simulated lost reply response");
            }
            if (posts.length === 3) await new Promise((resolve) => { releaseResponse = resolve; });
          }
          return response;
        };
        const detail = document.querySelector("#ticketDetailPane");
        setValue(detail.querySelector("[name='body']"), reply);
        if (withAttachment) {
          const { setAttachmentFiles } = await import("/static/attachments.js");
          setAttachmentFiles(detail.querySelector("input[type='file']"), [
            new File(["retry attachment"], "reply.txt", { type: "text/plain" })
          ]);
        }
        const submit = async () => {
          detail.querySelector("[data-comment-send]").click();
          await submitModal("Send this reply?");
        };
        await submit();
        await waitFor(() => posts.length === 1 && !detail.querySelector("[data-comment-send]").disabled, "retry after lost response");

        // Reopening must retain both the draft and its retry identity.
        closeSelectedTicket();
        await applyTicketRouteSelection(ticket.key);
        if (detail.querySelector("[name='body']").value !== reply) throw new Error("Lost response discarded the draft");
        detailReads = 0;
        failRefresh = true;
        await submit();
        await waitFor(() => posts.length === 2 && !failRefresh, "successful replay and failed list refresh");
        await waitFor(() => detail.querySelector("[name='body']").value === "", "confirmed reply clears the composer");
        if (detailReads !== 0) throw new Error("Sending a reply unnecessarily fetched the ticket again");
        if (!posts[0].key || posts[1].key !== posts[0].key || !posts[1].replayed) {
          throw new Error("Retry did not reuse the original idempotency key");
        }
        if (!detail.textContent.includes(reply) || !detail.querySelector("[data-comment-send]").disabled) {
          throw new Error("List refresh failure left the saved reply available to resend");
        }

        // Sending the same text intentionally is a new action after success.
        const sentBody = detail.querySelector("[name='body']");
        setValue(sentBody, reply);
        await submit();
        await waitFor(() => releaseResponse, "new reply awaiting its response");
        try {
          setValue(sentBody, "Next draft");
          if (!detail.querySelector("[data-comment-send]").disabled) {
            throw new Error("Editing the draft enabled a second send while the first was pending");
          }
        } finally {
          releaseResponse();
        }
        await waitFor(() => !sentBody.isConnected, "new reply rendered");
        if (detail.querySelector("[name='body']").value !== "Next draft") {
          throw new Error("Reply completion discarded edits made while sending");
        }
        if (posts[2].key === posts[0].key || posts[2].replayed) throw new Error("New reply reused a completed request key");
        const saved = await request(path);
        if (saved.comments.length !== 2 || saved.comments.some((comment) => comment.body !== reply)) {
          throw new Error("Retry duplicated a reply or suppressed the next reply");
        }
        const attachments = saved.comments.flatMap((comment) => comment.attachments || []);
        if (attachments.length !== (withAttachment ? 1 : 0)) throw new Error("Retry duplicated or lost an attachment");
        platform.fetch = originalFetch;
      }
    } finally {
      platform.fetch = originalFetch;
      startTicketRefreshLoop();
    }
  }, productID);
}
