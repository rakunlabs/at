<script lang="ts">
  import { push, location } from "svelte-spa-router";
  import { storeInfo } from "@/lib/store/store.svelte";
  import { getInfo } from "@/lib/api/gateway";
    import {
    Home,
    MessageSquare,
    Cpu,
    Key,
    Braces,
    Workflow,
    Activity,
    BookOpen,
    Settings,
    WandSparkles,
    SlidersHorizontal,
    Bot,
  } from "lucide-svelte";


  function navigate(e: MouseEvent, path: string) {
    if (e.ctrlKey || e.metaKey || e.shiftKey) return;
    e.preventDefault();
    push(path);
  }

  $effect(() => {
    if (!storeInfo.version) {
      getInfo().then((res) => {
        storeInfo.version = res.version || "";
        storeInfo.user = res.user || "";
        storeInfo.store_type = res.store_type || "";
      });
    }
  });
</script>

<div class="sidebar-bg border-r border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface flex flex-col h-svh transition-colors">
  <div class="flex-1 overflow-auto no-scrollbar">
    <a
      href="#/"
      onclick={(e) => navigate(e, "/")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Home size={14} />
      <span>Dashboard</span>
    </a>
    <a
      href="#/chat"
      onclick={(e) => navigate(e, "/chat")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/chat"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <MessageSquare size={14} />
      <span>Chat</span>
    </a>
    <a
      href="#/providers"
      onclick={(e) => navigate(e, "/providers")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/providers"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Cpu size={14} />
      <span>Providers</span>
    </a>
    <a
      href="#/tokens"
      onclick={(e) => navigate(e, "/tokens")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/tokens"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Key size={14} />
      <span>Tokens</span>
    </a>
    <a
      href="#/docs"
      onclick={(e) => navigate(e, "/docs")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/docs"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <BookOpen size={14} />
      <span>Docs</span>
    </a>
    <a
      href="#/settings"
      onclick={(e) => navigate(e, "/settings")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/settings"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Settings size={14} />
      <span>Settings</span>
    </a>
    <div>
      <span
        class="block p-2 text-[10px] font-medium text-gray-400 dark:text-dark-text-muted tracking-wider bg-gray-50 dark:bg-dark-base w-full border-b border-gray-200 dark:border-dark-border transition-colors"
        >Automation</span
      >
      <div class="border-l-4 border-gray-800 dark:border-accent">
        <a
          href="#/workflows"
          onclick={(e) => navigate(e, "/workflows")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/workflows" || $location.startsWith("/workflows/")
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <Workflow size={14} />
          <span>Workflows</span>
        </a>
        <a
          href="#/runs"
          onclick={(e) => navigate(e, "/runs")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/runs"
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <Activity size={14} />
          <span>Runs</span>
        </a>
        <a
          href="#/agents"
          onclick={(e) => navigate(e, "/agents")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/agents"
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <Bot size={14} />
          <span>Agents</span>
        </a>
        <a
          href="#/skills"
          onclick={(e) => navigate(e, "/skills")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/skills"
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <WandSparkles size={14} />
          <span>Skills</span>
        </a>
        <a
          href="#/variables"
          onclick={(e) => navigate(e, "/variables")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/variables"
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <Braces size={14} />
          <span>Variables</span>
        </a>
        <a
          href="#/node-configs"
          onclick={(e) => navigate(e, "/node-configs")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/node-configs"
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <SlidersHorizontal size={14} />
          <span>Node Configs</span>
        </a>
      </div>
    </div>
  </div>
  <div class="border-t border-gray-200 dark:border-dark-border p-3 text-[10px] text-gray-500 dark:text-dark-text-muted">
    {#if storeInfo.user}
      <div class="truncate font-medium text-gray-700 dark:text-dark-text-secondary" title={storeInfo.user}>{storeInfo.user}</div>
    {/if}
    <div class="flex items-center gap-2 mt-0.5">
      <span class="text-gray-600 dark:text-dark-text-muted">{storeInfo.version || 'v0.0.0'}</span>
    </div>
  </div>
</div>

<style>
  /* Hide scrollbar for Chrome, Safari and Opera */
  .no-scrollbar::-webkit-scrollbar {
    display: none;
  }

  /* Hide scrollbar for IE, Edge and Firefox */
  .no-scrollbar {
    -ms-overflow-style: none; /* IE and Edge */
    scrollbar-width: none; /* Firefox */
  }
</style>