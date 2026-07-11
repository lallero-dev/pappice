import { admin, customer } from "./fixtures.mjs";
import { runInPage, waitForDocumentReady } from "../tools/browser-page.mjs";

async function setupFirstAdmin(cdp) {
  await runInPage(cdp, async (input) => {
    const { setValue, waitFor } = pageTools();
    await waitFor(() => {
      const form = document.querySelector("#setupForm");
      return form && !form.hidden;
    }, "first-run setup form");
    const form = document.querySelector("#setupForm");
    setValue(form.querySelector("[name='email']"), input.email);
    setValue(form.querySelector("[name='display_name']"), input.displayName);
    setValue(form.querySelector("[name='password']"), input.password);
    form.requestSubmit();
    await waitFor(() => {
      const error = document.querySelector("#authError:not([hidden])")?.textContent;
      if (error) throw new Error(error);
      const app = document.querySelector("#appView");
      return app && !app.hidden && document.querySelector("#profileName")?.textContent.includes(input.displayName);
    }, "admin session after setup", 12000);
    return true;
  }, admin);
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
    await waitFor(() => document.querySelector("#userList .admin-row"), "accounts admin section");

    document.querySelector("#addUserButton").click();
    const root = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open]") ? rootNode : null;
    }, "new account modal");
    setValue(root.querySelector("[name='email']"), input.email);
    setValue(root.querySelector("[name='display_name']"), input.displayName);
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
        .find((item) => item.textContent.includes(input.email));
    }, "customer account row");
    const rowButtons = [...row.querySelectorAll("button")].map((button) => button.textContent.trim());
    if (rowButtons.includes("Reset") || rowButtons.includes("Delete")) {
      throw new Error("reset/delete controls should be inside the edit account modal");
    }
    row.querySelector("button").click();
    const editRoot = await waitFor(() => {
      const rootNode = modalRoot();
      return rootNode?.querySelector("dialog[open] h2")?.textContent.includes(input.displayName) ? rootNode : null;
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
    setValue(form.querySelector("[name='email']"), input.email);
    setValue(form.querySelector("[name='password']"), "wrong password");
    form.requestSubmit();
    await waitFor(() => {
      return form.contains(document.querySelector("#authError")) && !document.querySelector("#authError")?.hidden;
    }, "inline login error");
    setValue(form.querySelector("[name='email']"), input.email);
    setValue(form.querySelector("[name='password']"), input.password);
    form.requestSubmit();
    await waitFor(() => {
      const app = document.querySelector("#appView");
      return app && !app.hidden && document.querySelector("#profileName")?.textContent.includes(input.displayName);
    }, "admin session after login", 12000);
    return true;
  }, admin);
}

export {
  setupFirstAdmin,
  createCustomerAccount,
  completeCustomerSetup,
  logout,
  loginAsAdmin
};
