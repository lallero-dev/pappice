// Host services used by the shared interface. Other hosts can provide this
// module through an import map without changing application behavior.
export const platform = {
  fetch: (...args) => globalThis.fetch(...args),
  location: () => window.location,
  navigate(url, replace) {
    window.history[replace ? "replaceState" : "pushState"](null, "", url);
  },
  listen(handler) {
    window.addEventListener("popstate", handler);
    window.addEventListener("hashchange", handler);
  }
};
