# AT web interface

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users and purpose

AT provides a web interface for its LLM gateway, agents and workflows. The
Terminal surface serves installation administrators operating their Linux hosts.

## Terminal operating context

AT runs as root under systemd on Linux. Administrators need ordinary interactive
terminal tabs, saved across browser visits, without managing tmux themselves.
They choose a host and Linux user, and can save a default user for each host.
Multiple AT backends communicate through Alan. Shells survive AT restarts;
host reboots end running processes but preserve saved tab metadata.

## Constraints

Extend the existing Svelte UI and its light/dark theme. Keep terminal management
administrator-only and personal to the signed-in administrator. Use xterm.js for
the terminal display. Host terminals are independent of selected workspaces.
