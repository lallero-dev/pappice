import { state } from "./state.js";

export function accountName(user) {
  return user?.display_name || user?.email || "";
}

export function accountLabel(user) {
  const name = accountName(user);
  const email = user?.email || "";
  if (name && email && name !== email) return `${name} / ${email}`;
  return name || email || "Account";
}

export function currentProduct(productId) {
  return state.products.find((product) => product.id === productId) || null;
}

export function productDisplayName(product) {
  return product?.name || product?.key || (product?.id ? `Product ${product.id}` : "");
}

export function currentProductDetail() {
  return currentProduct(state.productDetailId);
}

export function isAdmin() {
  return state.user?.role === "admin";
}

export function isCustomer() {
  return state.user?.role === "customer";
}

export function canUseAssigneeFilter() {
  return !isCustomer();
}

export function productRole(productId) {
  return currentProduct(productId)?.role || "";
}

export function canManageProduct(productId) {
  return Boolean(productId) && !isCustomer() && (isAdmin() || productRole(productId) === "manager");
}

export function manageableProducts() {
  return state.products.filter((product) => canManageProduct(product.id));
}

export function canAccessProductsView() {
  return isAdmin() || manageableProducts().length > 0;
}

export function canCreateTicket(productId = state.ticketProductId) {
  if (!productId) return state.products.some((product) => canCreateTicket(product.id));
  return isAdmin() || ["manager", "staff", "customer"].includes(productRole(productId));
}

export function canCommentTicket(ticket = null) {
  return Boolean(ticket?.product_id) && canCreateTicket(ticket.product_id);
}

export function canEditTicket(ticket = null) {
  const productId = ticket?.product_id || state.ticketProductId;
  return Boolean(productId) && !isCustomer() && (isAdmin() || ["manager", "staff"].includes(productRole(productId)));
}
