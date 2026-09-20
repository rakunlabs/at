export const storeNavbar = $state({
  title: "",
  sideBarOpen: true
});

export const storeTheme = $state({
  mode: (localStorage.getItem("theme") as "light" | "dark") || "light",
});

function applyTheme(mode: "light" | "dark") {
  document.documentElement.classList.toggle("dark", mode === "dark");
}

$effect.root(() => {
  $effect(() => {
    applyTheme(storeTheme.mode);
    localStorage.setItem("theme", storeTheme.mode);
  });
});

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
