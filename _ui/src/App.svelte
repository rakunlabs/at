<script lang="ts">
  import Router from "svelte-spa-router";
  import { location } from "svelte-spa-router";
  import { storeNavbar } from "@/lib/store/store.svelte";
  import Sidebar from "@/lib/components/Sidebar.svelte";
  import SettingsSidebar from "@/lib/components/SettingsSidebar.svelte";
  import Navbar from "@/lib/components/Navbar.svelte";
  import Toast from "@/lib/components/Toast.svelte";
  import routes from "@/routes";
</script>

<Toast />

<div
  class={[
    "grid grid-flow-col h-full w-full relative bg-gray-50 dark:bg-dark-base transition-colors",
    storeNavbar.sideBarOpen ? "grid-cols-[9rem]" : "grid-cols-[0]",
  ]}
>
  <Sidebar />
  <div class="h-full w-full grid grid-rows-[2rem_1fr] min-h-0">
    <Navbar />
    <div class="overflow-y-auto min-h-0">
      {#if $location === '/settings' || $location.startsWith('/settings/')}
        <div class="grid grid-cols-[11rem_1fr] min-h-full">
          <SettingsSidebar />
          <div class="min-w-0">
            <Router {routes} />
          </div>
        </div>
      {:else}
        <Router {routes} />
      {/if}
    </div>
  </div>
</div>
