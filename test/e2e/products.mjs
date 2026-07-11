import { customer } from "./fixtures.mjs";
import { runInPage, waitForDocumentReady } from "../tools/browser-page.mjs";

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

async function addCustomerToProduct(cdp, productID) {
  await runInPage(cdp, async ({ productID: selectedProductID, customerEmail }) => {
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
    await waitFor(() => window.location.pathname === `/products/${selectedProductID}/general`, "product general route");
    await waitFor(() => {
      const title = document.querySelector("#productContextTitle")?.textContent.trim();
      return title === selectedProductName;
    }, "selected product context");
    await waitFor(() => {
      return document.querySelector("[data-product-section='general']")?.classList.contains("active") &&
        !document.querySelector("[data-product-panel='general']")?.hidden;
    }, "product general section");
    setValue(document.querySelector("#productGeneralDescription"), "E2E product description");
    document.querySelector("#productGeneralForm").requestSubmit();
    await waitFor(() => document.querySelector("#appAlertText")?.textContent.includes("Product updated."), "product update notice");
    const deleteProduct = await waitFor(() => {
      const button = document.querySelector("#deleteProductButton");
      const zone = document.querySelector("#productDangerZone");
      return button && zone && !zone.hidden ? button : null;
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
    document.querySelector("[data-product-section='members']").click();
    await waitFor(() => window.location.pathname === `/products/${selectedProductID}/members`, "product members route");
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
    const userOption = [...userSelect.options].find((option) => option.textContent.includes(customerEmail));
    if (!userOption) throw new Error(`customer ${customerEmail} missing from member account select`);
    const customerUserID = userOption.value;
    setValue(userSelect, userOption.value);
    setValue(root.querySelector("[name='role']"), "customer");
    root.querySelector("form").requestSubmit();
    await waitFor(() => {
      const error = modalRoot()?.querySelector(".error:not([hidden])")?.textContent;
      if (error) throw new Error(error);
      return !modalRoot()?.querySelector("dialog")?.open;
    }, "add member modal closed", 12000);
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
  }, { productID, customerEmail: customer.email });
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

export {
  selectFirstProduct,
  addCustomerToProduct,
  verifyProductRouteReload
};
