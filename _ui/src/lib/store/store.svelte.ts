interface NavbarChatPanels {
  conversationsOpen: boolean;
  sessionOpen: boolean;
  toggleConversations: () => void;
  toggleSession: () => void;
}

export const storeNavbar = $state({
  title: "",
  sideBarOpen: true,
  chatPanels: null as NavbarChatPanels | null
});

// The interface has a single dark theme. `.dark` stays on <html> so `dark:`
// variants and `:global(.dark)` rules keep applying everywhere.
document.documentElement.classList.add("dark");
localStorage.removeItem("theme");

export const storeInfo = $state({
  name: "AT",
  version: "",
  commit: "",
  build_date: "",
  user: "",
  store_type: "",
  workspace_root: "",
  assets_root: "",
});
