export const storeNavbar = $state({
  title: "",
  sideBarOpen: true
});

export const storeTheme = $state({
  mode: (localStorage.getItem("theme") as "light" | "dark") || "light",
});

let themeApplied = false;

// Theme swaps must repaint instantly. Without this, every `transition-colors` /
// `transition-all` element (navbar, dashboard cards, ...) cross-fades between
// palettes and the UI looks like it is smearing. `.theme-switching` kills all
// transitions for the duration of the class swap, then hands them back.
function applyTheme(mode: "light" | "dark") {
  const root = document.documentElement;
  const swap = () => root.classList.toggle("dark", mode === "dark");

  if (!themeApplied) {
    themeApplied = true;
    swap();
    return;
  }

  root.classList.add("theme-switching");
  swap();
  void root.offsetHeight; // force a style flush while transitions are disabled
  requestAnimationFrame(() => root.classList.remove("theme-switching"));
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
