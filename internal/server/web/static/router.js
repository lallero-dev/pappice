export function createRouter({
  adminSections = [],
  defaultAdminSection = "accounts",
  productSections = [],
  defaultProductSection = "members"
} = {}) {
  const config = { adminSections, defaultAdminSection, productSections, defaultProductSection };
  let last = locationKey();
  const current = (location = window.location) => parseLocation(location, config);
  return {
    accountLinkRoute: (location = window.location) => accountLinkRoute(location),
    current,
    navigate(route, { replace = false } = {}) {
      const next = buildLocation(route, config);
      if (next === locationKey()) return;
      window.history[replace ? "replaceState" : "pushState"](null, "", next);
      last = next;
    },
    listen(handler) {
      const notify = () => {
        const next = locationKey();
        if (next === last) return;
        last = next;
        handler(current());
      };
      window.addEventListener("popstate", notify);
      window.addEventListener("hashchange", notify);
    }
  };
}

function accountLinkRoute({ pathname }) {
  const match = pathname.match(/^\/account\/(setup|reset)\/([^/]+)\/?$/);
  return match ? { purpose: match[1], token: decodePart(match[2]) } : null;
}

function parseLocation({ pathname, hash }, config) {
  const trailingSlash = pathname.length > 1 && pathname.endsWith("/");
  const parts = pathname.split("/").filter(Boolean).map(decodePart);
  const ticketKey = ticketKeyFromHash(hash);
  const base = { normalize: trailingSlash || (Boolean(hash) && !ticketKey) };
  if (parts.length === 0 || parts[0] === "tickets") {
    return { ...base, view: "issues", ticketKey, normalize: base.normalize || parts.length !== 1 };
  }
  if (parts[0] === "admin") {
    const adminSection = section(parts[1], config.adminSections, config.defaultAdminSection);
    return {
      view: "admin",
      adminSection,
      normalize: trailingSlash || parts.length !== 2 || adminSection !== parts[1] || Boolean(hash)
    };
  }
  if (parts[0] === "products") return productRoute(parts, hash, trailingSlash, config);
  return { ...base, view: "issues", ticketKey, normalize: true };
}

function productRoute(parts, hash, trailingSlash, config) {
  if (parts.length === 1) {
    return { view: "product", productMode: "index", normalize: trailingSlash || Boolean(hash) };
  }
  const productId = Number(parts[1] || 0);
  const productSection = section(parts[2], config.productSections, config.defaultProductSection);
  return {
    view: "product",
    productMode: "detail",
    productId,
    productSection,
    normalize: trailingSlash ||
      !Number.isInteger(productId) ||
      productId < 1 ||
      parts.length !== 3 ||
      productSection !== parts[2] ||
      Boolean(hash)
  };
}

function buildLocation(route, config) {
  if (route.view === "admin") {
    return `/admin/${section(route.adminSection, config.adminSections, config.defaultAdminSection)}`;
  }
  if (route.view === "product") {
    if (route.productMode !== "detail" || Number(route.productId) < 1) return "/products";
    return `/products/${Number(route.productId)}/${section(route.productSection, config.productSections, config.defaultProductSection)}`;
  }
  const ticketKey = normalizeTicketKey(route.ticketKey);
  return ticketKey ? `/tickets#${encodeURIComponent(ticketKey)}` : "/tickets";
}

function ticketKeyFromHash(hash) {
  return normalizeTicketKey(decodePart(String(hash || "").slice(1)).replace(/^ticket[=:]/i, ""));
}

function normalizeTicketKey(value = "") {
  if (!/^[a-z][a-z0-9]{1,15}-[1-9][0-9]*$/i.test(value.trim())) return "";
  const [product, number] = value.trim().split("-");
  return `${product.toUpperCase()}-${number}`;
}

function section(value, allowed, fallback) {
  return allowed.includes(value) ? value : fallback;
}

function locationKey() {
  return `${window.location.pathname}${window.location.search}${window.location.hash}`;
}

function decodePart(value) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}
