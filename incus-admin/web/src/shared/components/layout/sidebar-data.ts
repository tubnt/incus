export interface NavItem {
  key: string;
  to: string;
  icon: string;
}

export interface NavGroup {
  key: string;
  titleKey: string;
  icon: string;
  items: NavItem[];
}

export const userSidebar: NavItem[] = [
  { key: "nav.dashboard", to: "/", icon: "LayoutDashboard" },
  { key: "nav.myVms", to: "/vms", icon: "Server" },
  { key: "nav.sshKeys", to: "/ssh-keys", icon: "Key" },
  { key: "nav.billing", to: "/billing", icon: "CreditCard" },
  { key: "nav.tickets", to: "/tickets", icon: "MessageSquare" },
  { key: "nav.apiTokens", to: "/api-tokens", icon: "KeyRound" },
  { key: "nav.settings", to: "/settings", icon: "Settings" },
];

export const adminSidebar: NavGroup[] = [
  {
    key: "monitoring",
    titleKey: "sidebar.group.monitoring",
    icon: "Activity",
    items: [
      { key: "nav.monitor", to: "/admin/monitoring", icon: "Activity" },
      { key: "nav.observability", to: "/admin/observability", icon: "BarChart3" },
      { key: "nav.ha", to: "/admin/ha", icon: "Shield" },
    ],
  },
  {
    key: "resources",
    titleKey: "sidebar.group.resources",
    icon: "ServerCog",
    items: [
      { key: "nav.allVms", to: "/admin/vms", icon: "ServerCog" },
      { key: "nav.createVm", to: "/admin/create-vm", icon: "Plus" },
      { key: "nav.storage", to: "/admin/storage", icon: "HardDrive" },
    ],
  },
  {
    key: "infrastructure",
    titleKey: "sidebar.group.infrastructure",
    icon: "Network",
    items: [
      { key: "nav.clusters", to: "/admin/clusters", icon: "Network" },
      { key: "nav.nodes", to: "/admin/nodes", icon: "Server" },
      { key: "nav.nodeOps", to: "/admin/node-ops", icon: "Terminal" },
      { key: "nav.ipPools", to: "/admin/ip-pools", icon: "Globe" },
      { key: "nav.ipRegistry", to: "/admin/ip-registry", icon: "MapPin" },
    ],
  },
  {
    key: "billing",
    titleKey: "sidebar.group.billing",
    icon: "ShoppingCart",
    items: [
      { key: "nav.products", to: "/admin/products", icon: "Package" },
      { key: "nav.orders", to: "/admin/orders", icon: "ShoppingCart" },
      { key: "nav.invoices", to: "/admin/invoices", icon: "FileText" },
    ],
  },
  {
    key: "userOps",
    titleKey: "sidebar.group.userOps",
    icon: "Users",
    items: [
      { key: "nav.users", to: "/admin/users", icon: "Users" },
      { key: "nav.adminTickets", to: "/admin/tickets", icon: "Ticket" },
      { key: "nav.auditLogs", to: "/admin/audit-logs", icon: "FileText" },
    ],
  },
];
