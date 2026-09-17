import { ready, startBackend } from "./transport.js";

const baseURL = new URL("./", import.meta.url);
const status = document.querySelector("#browserStatus");
function fail(message) {
  status.textContent = message;
  status.setAttribute("role", "alert");
}

document.querySelector("#browserReset").addEventListener("click", () => {
  if (confirm("Reset this private demo? All changes in this tab will be lost.")) {
    history.replaceState(null, "", baseURL);
    location.reload();
  }
});

startBackend(location.href, fail);
try {
  await ready;
  status.textContent = "Private demo · Reloading resets your data";
  document.querySelector("#browserReset").disabled = false;
  const login = document.querySelector("#loginForm");
  login.elements.email.value = "admin@example.test";
  login.elements.password.value = "pappice-demo";
  const accounts = document.createElement("p");
  accounts.className = "auth-note";
  accounts.textContent = "Try admin@example.test, staff@example.test or customer@example.test. Password: pappice-demo";
  login.append(accounts);
  await import("./static/app.js");
} catch (error) {
  fail(String(error?.message || error));
}
