# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Mixed teams sharing one self-hosted installation:

- **Platform / DevOps administrators** who run the gateway: providers, API tokens, budgets, routing profiles, trace privacy. Desktop, during the working day, often switching between AT, a terminal and an editor.
- **Developers and agent builders** who use Chats, Sessions, agents, workflows and Developer Spaces for long working sessions, testing models, tools and skills against real tasks.

Both groups use the Chats workbench (confirmed by the user, 2026-10-03).

## Product Purpose

AT is a self-hosted, OpenAI-compatible LLM gateway plus agent and workflow platform. One endpoint routes to many providers (OpenAI, Anthropic, Vertex, Gemini, Bedrock, Cohere, MiniMax, System 1 services) with fallback, budgets, tracing and governance; on top of it sit Chats, Sessions, agents, organizations, a DAG workflow engine, Studio and Developer Spaces. Success: a team can point every client at one gateway and also build and observe agentic work in the same place.

## Positioning

One installation is both the routing/governance layer and the place where agents run, so every chat turn, tool call and delegation is costed, budgeted and traced through the same gateway it was served by.

## Operating Context

- Chats is an agent-independent workbench: model picker, presets (personal and workspace), editable system prompt, skills, MCP sets, built-in tools, browser-local MCP servers and browser extensions; per-turn traces; artifacts from skill runs; todos; share links.
- Workspaces, roles and capability admission decide what each member may see and run.
- Admins live in Providers, Tokens, Usage and Traces; builders live in Chats, Sessions, Agents and Workflows.

## Capabilities and Constraints

- Svelte 5 + Vite + TailwindCSS 4 SPA under `_ui/`, hash routing, served from a configurable sub-path; PWA for phones.
- Must keep light and dark themes, keyboard access, and work on mobile.
- The user explicitly freed the visual system: no current color, corner or density choice is binding.

## Brand Commitments

- Name: AT. Existing `BrandLogo.svelte` mark.
- No visual commitments are binding (user: "Hiçbiri, serbest").

## Evidence on Hand

- Real product: providers, models, tools and traces are live data; demonstration content in prototypes must be labelled synthetic.
- No customer names, benchmarks or pricing claims exist to use.

## Product Principles

1. Every model call is accountable: who, which provider, what it cost, where its trace is.
2. Tools are powerful and therefore visible: the user always sees what can run and what ran.
3. One workbench for experts: dense, keyboard-first, never decorative at the expense of the task.
4. Governance is presentation of real admission, never a decorative switch.
