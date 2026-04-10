# Development Setup — Debian 13 (Trixie)

System dependencies required to build, test, and run AT on Debian 13.

## Prerequisites

```sh
sudo apt update && sudo apt install -y \
    build-essential \
    git \
    curl \
    wget \
    ca-certificates \
    gnupg
```

## Go 1.26

Debian's repositories may not ship Go 1.26. Install from the official tarball:

```sh
GO_VERSION=1.26.0
wget "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
rm "go${GO_VERSION}.linux-amd64.tar.gz"
```

Add to `~/.bashrc` or `~/.profile`:

```sh
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
```

Verify:

```sh
go version
# go version go1.26.0 linux/amd64
```

## Node.js 24 and pnpm 10

Required for building the Svelte UI (`_ui/`).

```sh
# Node.js 24 via NodeSource
curl -fsSL https://deb.nodesource.com/setup_24.x | sudo bash -
sudo apt install -y nodejs

# pnpm via corepack (ships with Node.js)
corepack enable
corepack prepare pnpm@10 --activate
```

Verify:

```sh
node --version   # v24.x.x
pnpm --version   # 10.x.x
```

## GoReleaser

Required for `make build` (snapshot builds and releases).

```sh
go install github.com/goreleaser/goreleaser/v2@latest
```

Or install via the official script:

```sh
curl -sfL https://goreleaser.com/static/run | bash
```

## golangci-lint

Required for `make lint`.

```sh
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

## Docker and Docker Compose

Required for `make env` (local PostgreSQL) and container builds.

```sh
# Docker
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
    https://download.docker.com/linux/debian trixie stable" | \
    sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# Allow running docker without sudo
sudo usermod -aG docker "$USER"
newgrp docker
```

## PostgreSQL Client (optional)

Only needed if you want to interact with the database directly (pg_dump/psql). The database itself runs in Docker via `make env`.

```sh
sudo apt install -y postgresql-client
```

## Summary

| Component                   | Version | Purpose                            |
| --------------------------- | ------- | ---------------------------------- |
| Go                          | 1.26    | Backend build and tests            |
| Node.js                     | 24      | UI build (`_ui/`)                  |
| pnpm                        | 10      | UI package manager                 |
| GoReleaser                  | latest  | Binary and container releases      |
| golangci-lint               | latest  | Go linting (`make lint`)           |
| Docker + Compose            | latest  | Local PostgreSQL, container builds |
| build-essential             | -       | C compiler (needed by CGO deps)    |
| git                         | -       | Source control, go module fetch    |
| curl, wget, ca-certificates | -       | Downloading tools                  |

## Quick ~~Start~~

After installing all dependencies:

```sh
# Clone and enter the repo
git clone https://github.com/rakunlabs/at.git && cd at

# Start local PostgreSQL
make env

# Install UI deps and run dev server
make install-ui
make run-ui          # Terminal 1 — UI at localhost:3000

# Run backend
make run             # Terminal 2 — API at localhost:8080

# Run tests
make test

# Lint
make lint

# Full build (UI + Go binary via goreleaser)
make build
```
