<script lang="ts">
  import Router from "svelte-spa-router";
  import { storeNavbar } from "@/lib/store/store.svelte";
  import Sidebar from "@/lib/components/Sidebar.svelte";
  import Navbar from "@/lib/components/Navbar.svelte";
  import Toast from "@/lib/components/Toast.svelte";
  import routes from "@/routes";
</script>

<Toast />

<div
  class={[
    "grid grid-flow-col h-full w-full relative overflow-y-auto bg-gray-50",
    storeNavbar.sideBarOpen ? "grid-cols-[9rem]" : "grid-cols-[0]",
  ]}
>
  <Sidebar />
  <div class="h-full w-full grid grid-rows-[2rem]">
    <Navbar />
    <Router {routes} />
  </div>
</div>
