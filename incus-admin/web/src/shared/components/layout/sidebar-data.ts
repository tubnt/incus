export interface NavItem {
  key: string;
  to: string;
  icon: string;
  adminOnly?: boolean;
}

export interface NavGroup {
  titleKey: string;
  items: NavItem[];
}

export const sidebarGroups: NavGroup[] = [
  {
    titleKey: "",
    items: [
      { key: "nav.dashboard", to: "/", icon: "LayoutDashboard" },
    ],
  },
  {
    titleKey: "user",
    items: [
      { key: "nav.myVms", to: "/vms", icon: "Server" },
      { key: "nav.sshKeys", to: "/ssh-keys", icon: "Key" },
      { key: "nav.billing", to: "/billing", icon: "CreditCard" },
      { key: "nav.tickets", to: "/tickets", icon: "MessageSquare" },
      { key: "nav.apiTokens", to: "/api-tokens", icon: "KeyRound" },
    ],
  },
  {
    titleKey: "admin",
    items: [
      { key: "nav.clusters", to: "/admin/clusters", icon: "Network", adminOnly: true },
      { key: "nav.allVms", to: "/admin/vms", icon: "ServerCog", adminOnly: true },
      { key: "nav.monitor", to: "/admin/monitoring", icon: "Activity", adminOnly: true },
      { key: "HA", to: "/admin/ha", icon: "Shield", adminOnly: true },
      { key: "Storage", to: "/admin/storage", icon: "HardDrive", adminOnly: true },
      { key: "nav.ipPools", to: "/admin/ip-pools", icon: "Globe", adminOnly: true },
      { key: "IP Registry", to: "/admin/ip-registry", icon: "MapPin", adminOnly: true },
      { key: "nav.users", to: "/admin/users", icon: "Users", adminOnly: true },
      { key: "nav.products", to: "/admin/products", icon: "Package", adminOnly: true },
      { key: "nav.orders", to: "/admin/orders", icon: "ShoppingCart", adminOnly: true },
      { key: "nav.invoices", to: "/admin/invoices", icon: "FileText", adminOnly: true },
      { key: "nav.adminTickets", to: "/admin/tickets", icon: "Ticket", adminOnly: true },
      { key: "nav.auditLogs", to: "/admin/audit-logs", icon: "FileText", adminOnly: true },
      { key: "nav.nodes", to: "/admin/nodes", icon: "Server", adminOnly: true },
      { key: "Node Ops", to: "/admin/node-ops", icon: "Terminal", adminOnly: true },
      { key: "Observability", to: "/admin/observability", icon: "BarChart3", adminOnly: true },
      { key: "nav.createVm", to: "/admin/create-vm", icon: "Plus", adminOnly: true },
    ],
  },
];
