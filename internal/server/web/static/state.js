import { createRouter } from "./router.js";

export const DEFAULT_ADMIN_SECTION = "accounts";
export const ADMIN_SECTIONS = [DEFAULT_ADMIN_SECTION, "tokens", "webhooks", "email", "maintenance", "audit"];
export const DEFAULT_PRODUCT_SECTION = "general";
export const PRODUCT_SECTIONS = [DEFAULT_PRODUCT_SECTION, "members", "webhooks", "deliveries"];
export const DEFAULT_TICKET_STATUSES = ["new", "assigned"];
export const TICKET_AUTOSAVE_DELAY_MS = 450;
export const TICKET_PAGE_SIZE = 50;
export const TICKET_REFRESH_INTERVAL_MS = 5000;
export const TICKET_SORT_LABELS = {
  updated_at: "Updated",
  created_at: "Created",
  priority: "Priority",
  status: "Status",
  title: "Title"
};

export const router = createRouter({
  adminSections: ADMIN_SECTIONS,
  defaultAdminSection: DEFAULT_ADMIN_SECTION,
  productSections: PRODUCT_SECTIONS,
  defaultProductSection: DEFAULT_PRODUCT_SECTION
});

export const state = {
  tickets: [],
  ticketCounts: { all: 0 },
  ticketPage: {
    limit: TICKET_PAGE_SIZE,
    offset: 0,
    hasMore: false
  },
  assignees: [],
  products: [],
  users: [],
  members: [],
  webhooks: [],
  globalWebhooks: [],
  emailNotifications: [],
  auditEvents: [],
  maintenance: null,
  emailStats: null,
  emailEnabled: false,
  notificationDelaySeconds: 0,
  deliveries: [],
  tokens: [],
  branding: {
    name: "Pappice",
    subtitle: "customer support",
    mark: "P",
    color: "#5bb974"
  },
  user: null,
  accountLink: null,
  csrf: "",
  view: "tickets",
  adminSection: DEFAULT_ADMIN_SECTION,
  productSection: DEFAULT_PRODUCT_SECTION,
  productMode: "index",
  productDetailId: null,
  ticketProductId: null,
  selectedId: null,
  selectedTicket: null,
  selectedUnreadBoundary: null,
  renderedTicketDetailId: null,
  sort: {
    key: "updated_at",
    dir: "desc"
  },
  meta: {
    statuses: [],
    priorities: [],
    roles: [],
    productRoles: [],
    webhookEvents: [],
    uploads: {
      max_size_bytes: 10 * 1024 * 1024,
      max_files: 5,
      allowed_types: []
    }
  },
  filters: {
    q: "",
    statuses: [...DEFAULT_TICKET_STATUSES],
    assigneeUserId: "",
    statusCustomized: false,
    unread: false
  },
  emailPage: {
    q: "",
    status: "",
    limit: 25,
    offset: 0,
    total: 0
  },
  auditPage: {
    q: "",
    limit: 25,
    offset: 0,
    total: 0
  }
};

export const els = {
  appAlert: document.querySelector("#appAlert"),
  appAlertText: document.querySelector("#appAlertText"),
  appAlertClose: document.querySelector("#appAlertClose"),
  brandMark: document.querySelector("#brandMark"),
  brandName: document.querySelector("#brandName"),
  brandSubtitle: document.querySelector("#brandSubtitle"),
  topNav: document.querySelector("#topNav"),
  ticketsTab: document.querySelector("#ticketsTab"),
  ticketsUnreadBadge: document.querySelector("#ticketsUnreadBadge"),
  productTab: document.querySelector("#productTab"),
  adminTab: document.querySelector("#adminTab"),
  profileMenu: document.querySelector("#profileMenu"),
  profileButton: document.querySelector("#profileButton"),
  profileAvatar: document.querySelector("#profileAvatar"),
  profileName: document.querySelector("#profileName"),
  profileRole: document.querySelector("#profileRole"),
  profileMenuName: document.querySelector("#profileMenuName"),
  profileEmail: document.querySelector("#profileEmail"),
  profilePopover: document.querySelector("#profilePopover"),
  profileEditButton: document.querySelector("#profileEditButton"),
  changePasswordButton: document.querySelector("#changePasswordButton"),
  logoutButton: document.querySelector("#logoutButton"),
  newTicketButton: document.querySelector("#newTicketButton"),
  authView: document.querySelector("#authView"),
  authError: document.querySelector("#authError"),
  setupForm: document.querySelector("#setupForm"),
  loginForm: document.querySelector("#loginForm"),
  accountLinkForm: document.querySelector("#accountLinkForm"),
  accountLinkTitle: document.querySelector("#accountLinkTitle"),
  accountLinkHelp: document.querySelector("#accountLinkHelp"),
  accountLinkUser: document.querySelector("#accountLinkUser"),
  accountLinkSubmit: document.querySelector("#accountLinkSubmit"),
  appView: document.querySelector("#appView"),
  ticketView: document.querySelector("#ticketView"),
  adminView: document.querySelector("#adminView"),
  adminSectionButtons: Array.from(document.querySelectorAll("[data-admin-section]")),
  adminSectionPanels: Array.from(document.querySelectorAll("[data-admin-panel]")),
  productView: document.querySelector("#productView"),
  productSectionButtons: Array.from(document.querySelectorAll("[data-product-section]")),
  productSectionPanels: Array.from(document.querySelectorAll("[data-product-panel]")),
  productIndexPanel: document.querySelector("#productIndexPanel"),
  productIndexList: document.querySelector("#productIndexList"),
  productDetailView: document.querySelector("#productDetailView"),
  productContextTitle: document.querySelector("#productContextTitle"),
  productContextMeta: document.querySelector("#productContextMeta"),
  productGeneralForm: document.querySelector("#productGeneralForm"),
  productGeneralKey: document.querySelector("#productGeneralKey"),
  productGeneralName: document.querySelector("#productGeneralName"),
  productGeneralDescription: document.querySelector("#productGeneralDescription"),
  saveProductButton: document.querySelector("#saveProductButton"),
  productDangerZone: document.querySelector("#productDangerZone"),
  deleteProductButton: document.querySelector("#deleteProductButton"),
  ticketList: document.querySelector("#ticketList"),
  ticketDetailPane: document.querySelector("#ticketDetailPane"),
  searchInput: document.querySelector("#searchInput"),
  ticketFilterButton: document.querySelector("#ticketFilterButton"),
  ticketFilterBadge: document.querySelector("#ticketFilterBadge"),
  ticketFilterPopover: document.querySelector("#ticketFilterPopover"),
  ticketPopoverBackdrop: document.querySelector("#ticketPopoverBackdrop"),
  clearTicketFiltersButton: document.querySelector("#clearTicketFiltersButton"),
  productFilter: document.querySelector("#productFilter"),
  assigneeFilter: document.querySelector("#assigneeFilter"),
  unreadFilter: document.querySelector("#unreadFilter"),
  ticketSortButton: document.querySelector("#ticketSortButton"),
  ticketSortPopover: document.querySelector("#ticketSortPopover"),
  ticketSortLabel: document.querySelector("#ticketSortLabel"),
  ticketSortSelect: document.querySelector("#ticketSortSelect"),
  statusFilterList: document.querySelector("#statusFilterList"),
  addProductButton: document.querySelector("#addProductButton"),
  addUserButton: document.querySelector("#addUserButton"),
  userList: document.querySelector("#userList"),
  createTokenButton: document.querySelector("#createTokenButton"),
  tokenResult: document.querySelector("#tokenResult"),
  tokenList: document.querySelector("#tokenList"),
  addMemberButton: document.querySelector("#addMemberButton"),
  memberList: document.querySelector("#memberList"),
  addWebhookButton: document.querySelector("#addWebhookButton"),
  webhookList: document.querySelector("#webhookList"),
  addGlobalWebhookButton: document.querySelector("#addGlobalWebhookButton"),
  globalWebhookList: document.querySelector("#globalWebhookList"),
  sendTestEmailButton: document.querySelector("#sendTestEmailButton"),
  emailSearchInput: document.querySelector("#emailSearchInput"),
  emailStatusFilter: document.querySelector("#emailStatusFilter"),
  emailOverview: document.querySelector("#emailOverview"),
  emailList: document.querySelector("#emailList"),
  emailPager: document.querySelector("#emailPager"),
  maintenanceOverview: document.querySelector("#maintenanceOverview"),
  auditSearchInput: document.querySelector("#auditSearchInput"),
  auditList: document.querySelector("#auditList"),
  auditPager: document.querySelector("#auditPager"),
  deliveryList: document.querySelector("#deliveryList"),
  modalHost: document.querySelector("#modalHost")
};

export const shortDateFormatter = new Intl.DateTimeFormat(undefined, {
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  month: "short"
});

export const fullDateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short"
});
