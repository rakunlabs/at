import { defineConfig } from "vitepress";

export default defineConfig({
  title: "AT",
  description: "AI Agent Platform Documentation",
  themeConfig: {
    nav: [{ text: "Home", link: "/" }],
    sidebar: [
      {
        text: "Guide",
        items: [
          { text: "Getting Started", link: "/getting-started" },
          { text: "Setup", link: "/setup" },
          { text: "Runtime Dependencies", link: "/runtime" },
          { text: "Bots", link: "/bots" },
          { text: "Task Delegation", link: "/task-delegation" },
        ],
      },
    ],
    socialLinks: [
      { icon: "github", link: "https://github.com/rakunlabs/at" },
    ],
  },
});
