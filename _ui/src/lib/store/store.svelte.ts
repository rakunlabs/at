export const storeNavbar = $state({
  title: "",
  sideBarOpen: true
});

export const storeTheme = $state({
  mode: (localStorage.getItem("theme") as "light" | "dark") || "light",
});

$effect.root(() => {
  $effect(() => {
    if (storeTheme.mode === "dark") {
      document.documentElement.classList.add("dark");
    } else {
      document.documentElement.classList.remove("dark");
    }
    localStorage.setItem("theme", storeTheme.mode);
  });
});

export const storeInfo = $state({
  name: "AT",
  version: "",
  user: "",
  store_type: "",
});
