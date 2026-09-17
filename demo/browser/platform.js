import { localFetch } from "./transport.js";

export const platform = {
  fetch: localFetch,
  location() {
    const route = location.hash.slice(1);
    return new URL(location.origin + (route.startsWith("/") ? route : "/tickets"));
  },
  navigate(url, replace) {
    const next = `${location.pathname}${location.search}#${url}`;
    history[replace ? "replaceState" : "pushState"](null, "", next);
  },
  listen(handler) {
    window.addEventListener("popstate", handler);
    window.addEventListener("hashchange", handler);
  }
};
