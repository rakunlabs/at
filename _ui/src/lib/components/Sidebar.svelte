<script lang="ts">
  import { push, location } from "svelte-spa-router";
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
    Wand2,
    SlidersHorizontal,
  } from "lucide-svelte";

  function navigate(e: MouseEvent, path: string) {
    if (e.ctrlKey || e.metaKey || e.shiftKey) return;
    e.preventDefault();
    push(path);
  }
</script>

<div class="sidebar-bg border-r border-gray-200 bg-white">
  <div class="sticky top-0 overflow-auto max-h-svh no-scrollbar">
    <a
      href="#/"
      onclick={(e) => navigate(e, "/")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors",
        $location === "/"
          ? "bg-gray-900 text-white"
          : "text-gray-700 hover:bg-gray-100",
      ]}
    >
      <Home size={14} />
      <span>Dashboard</span>
    </a>
    <a
      href="#/chat"
      onclick={(e) => navigate(e, "/chat")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors",
        $location === "/chat"
          ? "bg-gray-900 text-white"
          : "text-gray-700 hover:bg-gray-100",
      ]}
    >
      <MessageSquare size={14} />
      <span>Chat</span>
    </a>
    <a
      href="#/providers"
      onclick={(e) => navigate(e, "/providers")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors",
        $location === "/providers"
          ? "bg-gray-900 text-white"
          : "text-gray-700 hover:bg-gray-100",
      ]}
    >
      <Cpu size={14} />
      <span>Providers</span>
    </a>
    <div>
      <span
        class="block p-2 text-[10px] font-medium text-gray-400 tracking-wider bg-gray-50 w-full border-b border-gray-200 transition-colors"
        >Automation</span
      >
      <div class="border-l-4 border-gray-800">
        <a
          href="#/workflows"
          onclick={(e) => navigate(e, "/workflows")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 transition-colors",
            $location === "/workflows" || $location.startsWith("/workflows/")
              ? "bg-gray-900 text-white"
              : "text-gray-700 hover:bg-gray-100",
          ]}
        >
          <Workflow size={14} />
          <span>Workflows</span>
        </a>
        <a
          href="#/runs"
          onclick={(e) => navigate(e, "/runs")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 transition-colors",
            $location === "/runs"
              ? "bg-gray-900 text-white"
              : "text-gray-700 hover:bg-gray-100",
          ]}
        >
          <Activity size={14} />
          <span>Runs</span>
        </a>
        <a
          href="#/skills"
          onclick={(e) => navigate(e, "/skills")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 transition-colors",
            $location === "/skills"
              ? "bg-gray-900 text-white"
              : "text-gray-700 hover:bg-gray-100",
          ]}
        >
          <Wand2 size={14} />
          <span>Skills</span>
        </a>
        <a
          href="#/variables"
          onclick={(e) => navigate(e, "/variables")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 transition-colors",
            $location === "/variables"
              ? "bg-gray-900 text-white"
              : "text-gray-700 hover:bg-gray-100",
          ]}
        >
          <Braces size={14} />
          <span>Variables</span>
        </a>
        <a
          href="#/node-configs"
          onclick={(e) => navigate(e, "/node-configs")}
          class={[
            "flex items-center gap-2 px-2 h-8 text-sm border-b border-gray-200 transition-colors",
            $location === "/node-configs"
              ? "bg-gray-900 text-white"
              : "text-gray-700 hover:bg-gray-100",
          ]}
        >
          <SlidersHorizontal size={14} />
          <span>Node Configs</span>
        </a>
      </div>
    </div>
    <a
      href="#/tokens"
      onclick={(e) => navigate(e, "/tokens")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors",
        $location === "/tokens"
          ? "bg-gray-900 text-white"
          : "text-gray-700 hover:bg-gray-100",
      ]}
    >
      <Key size={14} />
      <span>Tokens</span>
    </a>
    <a
      href="#/docs"
      onclick={(e) => navigate(e, "/docs")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors",
        $location === "/docs"
          ? "bg-gray-900 text-white"
          : "text-gray-700 hover:bg-gray-100",
      ]}
    >
      <BookOpen size={14} />
      <span>Docs</span>
    </a>
    <a
      href="#/settings"
      onclick={(e) => navigate(e, "/settings")}
      class={[
        "flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors",
        $location === "/settings"
          ? "bg-gray-900 text-white"
          : "text-gray-700 hover:bg-gray-100",
      ]}
    >
      <Settings size={14} />
      <span>Settings</span>
    </a>
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
