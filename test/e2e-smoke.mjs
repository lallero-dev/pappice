#!/usr/bin/env node

import { waitForDocumentReady } from "./tools/browser-page.mjs";
import { startLocalPappice } from "./tools/local-pappice.mjs";
import {
  completeCustomerSetup,
  createCustomerAccount,
  loginAsAdmin,
  logout,
  setupFirstAdmin
} from "./e2e/accounts.mjs";
import {
  addCustomerToProduct,
  selectFirstProduct,
  verifyProductRouteReload
} from "./e2e/products.mjs";
import {
  createCustomerTicket,
  staffReplyAndResolve,
  verifyFixedTicketLayout,
  verifySinglePaneTicketFlow,
  verifyTicketHashRoute
} from "./e2e/tickets.mjs";
import {
  verifyAuditLog,
  verifyEmailOutbox
} from "./e2e/admin-signals.mjs";

let app = null;

main().catch(async (error) => {
  console.error("");
  console.error("E2E smoke test failed:");
  console.error(error?.stack || error);
  await cleanup();
  process.exit(1);
});

async function main() {
  app = await startLocalPappice({
    keepTemp: Boolean(process.env.PAPPICE_E2E_KEEP_TMP),
    tempPrefix: "pappice-e2e-"
  });

  const { page } = app;
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

async function cleanup() {
  if (!app) return;
  const current = app;
  app = null;
  await current.cleanup();
}
