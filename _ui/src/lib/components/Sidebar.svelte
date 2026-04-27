<script lang="ts">
  import { push, location } from "svelte-spa-router";
  import { storeInfo } from "@/lib/store/store.svelte";
  import { getInfo } from "@/lib/api/gateway";
    import {
    MessageSquare,
    MessagesSquare,
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
    Radio,
    Database,
    Server,
    Layers,
    House,
    Building2,
    Globe,
    Clock,
    ClipboardList,
    ScrollText,
    Receipt,
    BarChart3,
    Plug,
    FolderOpen,
    Package,
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
        storeInfo.name = res.name || "AT";
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
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <House size={14} />
      <span>Dashboard</span>
    </a>
    <a
      href="#/chat"
      onclick={(e) => navigate(e, "/chat")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/chat"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <MessageSquare size={14} />
      <span>Chat</span>
    </a>
    <a
      href="#/sessions"
      onclick={(e) => navigate(e, "/sessions")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/sessions"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <MessagesSquare size={14} />
      <span>Sessions</span>
    </a>
    <a
      href="#/providers"
      onclick={(e) => navigate(e, "/providers")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/providers"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Cpu size={14} />
      <span>Providers</span>
    </a>
    <a
      href="#/rag"
      onclick={(e) => navigate(e, "/rag")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/rag"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Database size={14} />
      <span>RAG</span>
    </a>
    <a
      href="#/mcp-servers"
      onclick={(e) => navigate(e, "/mcp-servers")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/mcp-servers"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Server size={14} />
      <span>MCP Servers</span>
    </a>
    <a
      href="#/mcps"
      onclick={(e) => navigate(e, "/mcps")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/mcps"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Layers size={14} />
      <span>MCP</span>
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
      href="#/integrations"
      onclick={(e) => navigate(e, "/integrations")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/integrations"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Package size={14} />
      <span>Integrations</span>
    </a>
    <a
      href="#/connections"
      onclick={(e) => navigate(e, "/connections")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/connections"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Plug size={14} />
      <span>Connections</span>
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
      href="#/bots"
      onclick={(e) => navigate(e, "/bots")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/bots"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Radio size={14} />
      <span>Bots</span>
    </a>
    <a
      href="#/tokens"
      onclick={(e) => navigate(e, "/tokens")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/tokens"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <Key size={14} />
      <span>Tokens</span>
    </a>
    <a
      href="#/files"
      onclick={(e) => navigate(e, "/files")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/files"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <FolderOpen size={14} />
      <span>Files</span>
    </a>
    <a
      href="#/audit"
      onclick={(e) => navigate(e, "/audit")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/audit"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <ScrollText size={14} />
      <span>Audit</span>
    </a>
    <a
      href="#/guides"
      onclick={(e) => navigate(e, "/guides")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/guides"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <BookOpen size={14} />
      <span>Guides</span>
    </a>
    <a
      href="#/docs"
      onclick={(e) => navigate(e, "/docs")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/docs"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <BookOpen size={14} />
      <span>Docs</span>
    </a>
    <a
      href="#/usage"
      onclick={(e) => navigate(e, "/usage")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
        $location === "/usage"
          ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
          : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
      ]}
    >
      <BarChart3 size={14} />
      <span>Usage</span>
    </a>
    <a
      href="#/settings"
      onclick={(e) => navigate(e, "/settings")}
      class={[
        "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
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
        <a
          href="#/webhooks"
          onclick={(e) => navigate(e, "/webhooks")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/webhooks"
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <Globe size={14} />
          <span>Webhooks</span>
        </a>
        <a
          href="#/crons"
          onclick={(e) => navigate(e, "/crons")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/crons"
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <Clock size={14} />
          <span>Crons</span>
        </a>
      </div>
    </div>
    <div>
      <span
        class="block p-2 text-[10px] font-medium text-gray-400 dark:text-dark-text-muted tracking-wider bg-gray-50 dark:bg-dark-base w-full border-b border-gray-200 dark:border-dark-border transition-colors"
        >Organization</span
      >
      <div class="border-l-4 border-gray-800 dark:border-accent">
        <a
          href="#/organizations"
          onclick={(e) => navigate(e, "/organizations")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/organizations" || $location.startsWith("/organizations/")
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <Building2 size={14} />
          <span>Organizations</span>
        </a>
        <a
          href="#/tasks"
          onclick={(e) => navigate(e, "/tasks")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/tasks" || $location.startsWith("/tasks/")
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <ClipboardList size={14} />
          <span>Tasks</span>
        </a>
        <a
          href="#/cost-events"
          onclick={(e) => navigate(e, "/cost-events")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 dark:border-dark-border transition-colors",
            $location === "/cost-events"
              ? "bg-gray-900 text-white dark:bg-accent dark:text-white"
              : "text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated",
          ]}
        >
          <Receipt size={14} />
          <span>Cost Events</span>
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