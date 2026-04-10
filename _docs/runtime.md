# Runtime Dependencies — Debian 13 (Trixie)

Programs and libraries AT may invoke at runtime. Not all are required — install what you need based on the features you use.

## Required

These are needed for core AT functionality.

```sh
sudo apt update && sudo apt install -y \
    bash \
    git \
    curl \
    ca-certificates \
    openssh-client
```

| Program           | Purpose                                                                                              |
| ----------------- | ---------------------------------------------------------------------------------------------------- |
| `bash`            | Shell command execution for builtin tools, skill handlers, workflow bash handlers, MCP tool handlers |
| `git`             | Repository cloning/fetching for RAG sync, workflow git nodes, MCP code intelligence                  |
| `curl`            | HTTP requests from bash handlers, health checks                                                      |
| `ca-certificates` | TLS certificate verification for HTTPS connections                                                   |
| `openssh-client`  | Git SSH authentication (`GIT_SSH_COMMAND`)                                                           |

## Recommended

Used by common features. Install based on your use case.

```sh
sudo apt install -y \
    patch \
    ripgrep \
    sed \
    docker.io
```

| Program   | Purpose                                                                           |
| --------- | --------------------------------------------------------------------------------- |
| `patch`   | Builtin `file_patch` tool — applies unified diffs to files                        |
| `ripgrep` | Fast content search, available to bash handlers                                   |
| `sed`     | Stream editing, available to bash handlers                                        |
| `docker`  | Per-organization isolated container management (create, exec, inspect, start, rm) |

## Agent Runtime (containerized agents)

When using organization-scoped agent containers (`Dockerfile.agent-runtime`), the following are installed inside the container. You do **not** need these on the host unless running agents without container isolation.

```sh
# System packages
sudo apt install -y \
    ffmpeg \
    wget \
    jq \
    imagemagick \
    poppler-utils

# Node.js 24 (for MCP servers like Playwright)
curl -fsSL https://deb.nodesource.com/setup_24.x | sudo bash -
sudo apt install -y nodejs

# Python 3.13+ with pip
sudo apt install -y python3 python3-pip

# UV package manager (fast pip alternative)
pip install --break-system-packages uv

# Playwright + Chromium
npx -y playwright install --with-deps chromium
```

| Program          | Purpose                                                               |
| ---------------- | --------------------------------------------------------------------- |
| `ffmpeg`         | Audio/video processing for agent tasks                                |
| `wget`           | File downloads from agent scripts                                     |
| `jq`             | JSON processing in shell scripts                                      |
| `imagemagick`    | Image manipulation (convert, identify, etc.)                          |
| `poppler-utils`  | PDF processing (pdftotext, pdftoppm, etc.)                            |
| `nodejs` / `npx` | MCP server execution (e.g., Playwright)                               |
| `python3`        | Python script execution in workflow exec nodes, whisper transcription |
| `uvx`            | Run Python packages without install (openai-whisper)                  |
| `chromium`       | Headless browser via Playwright                                       |

## LSP Servers (optional)

Used by the builtin LSP tools for code intelligence. Install only the languages you need.

```sh
# Go
go install golang.org/x/tools/gopls@latest

# TypeScript / JavaScript
npm install -g typescript-language-server typescript

# Python
npm install -g pyright

# Rust
# Install via rustup: https://rustup.rs
rustup component add rust-analyzer

# C / C++
sudo apt install -y clangd

# Java
# Install Eclipse JDT Language Server: https://github.com/eclipse-jdtls/eclipse.jdt.ls
```

| Program                      | Language        |
| ---------------------------- | --------------- |
| `gopls`                      | Go              |
| `typescript-language-server` | TypeScript / JS |
| `pyright-langserver`         | Python          |
| `rust-analyzer`              | Rust            |
| `clangd`                     | C / C++         |
| `jdtls`                      | Java            |

## MCP Stdio Servers (user-configured)

AT can launch arbitrary MCP servers as subprocesses via stdin/stdout. The required binary depends on your MCP configuration — any command specified in `MCPUpstream.Command` must be available in `$PATH`.

## Summary

| Category        | Programs                                                                                        | Required                 |
| --------------- | ----------------------------------------------------------------------------------------------- | ------------------------ |
| Core            | `bash`, `git`, `curl`, `openssh-client`, `ca-certificates`                                      | Yes                      |
| File operations | `patch`, `ripgrep`, `sed`                                                                       | Recommended              |
| Containers      | `docker`                                                                                        | If using agent isolation |
| Agent runtime   | `ffmpeg`, `wget`, `jq`, `imagemagick`, `poppler-utils`, `python3`, `nodejs`, `chromium`         | Inside agent containers  |
| LSP servers     | `gopls`, `typescript-language-server`, `pyright-langserver`, `rust-analyzer`, `clangd`, `jdtls` | Per language             |
| MCP servers     | User-configured                                                                                 | Per config               |
