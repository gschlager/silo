# silo

Secure isolated local environments for AI coding agents.

`silo` creates development environments using [Incus](https://linuxcontainers.org/incus/) system containers, providing full network and service isolation while keeping your existing workflow (host-side IDE, git client, DB inspector) intact via bind mounts and port forwarding.

## Why

AI coding agents run with your full user permissions. They can read any file, connect to any local service, and access any credential on your machine. `silo` gives each project its own isolated container where agents can work freely without risking your host system.

## Quick start

```bash
# Install Incus (if not already installed)
# See https://linuxcontainers.org/incus/docs/main/installing/

# Install silo
go install github.com/gschlager/silo/cmd/silo@latest

# Generate a project config with AI
cd your-project
silo init

# Start the environment
silo up

# Run an AI agent
silo ra
```

`silo init` spins up a temporary container, uses an AI agent to analyze your project, and generates a `.silo.yml` configuration file. `silo up` provisions the environment, and `silo ra` launches your default agent inside it.

## How it works

```
┌─────────────────────────────────────────────────────────┐
│ HOST                                                    │
│                                                         │
│  IDE ──────────┐                                        │
│  Git client ───┤── ~/project (real files)               │
│  DB inspector ─┘         │                              │
│                          │ bind mount                   │
│  DB client ── localhost:15432   │                        │
│                    │            │                        │
│                    │ port       │                        │
│                    │ forward    │                        │
│           ┌────────┼────────────┼──────────────┐        │
│           │ INCUS CONTAINER     │              │        │
│           │                     ▼              │        │
│           │           /workspace (shared)      │        │
│           │                                    │        │
│           │  Claude / Codex (agent)            │        │
│           │                                    │        │
│           │  postgresql ── :5432               │        │
│           │  redis ─────── :6379               │        │
│           │                                    │        │
│           │  ✗ No route to host localhost      │        │
│           │  ✓ Internet access (APIs, deps)    │        │
│           └────────────────────────────────────┘        │
└─────────────────────────────────────────────────────────┘
```

- The project directory is shared via bind mount — edits are instantly visible on both sides.
- Services (Postgres, Redis) run inside the container, isolated from host services.
- Port forwarding exposes container services to host tools.
- The container has no route to the host's localhost ports.

## Project configuration

Create a `.silo.yml` in your project root (or run `silo init` to generate one):

```yaml
# silo project configuration
# https://github.com/gschlager/silo#project-configuration

# Base image (default: fedora/43)
image: fedora/43

# Commands run once on first provisioning (as dev user with sudo)
setup:
  - sudo dnf install -y postgresql16-server redis ruby nodejs
  - sudo systemctl enable --now postgresql redis
  - bundle install
  - bin/rails db:create
  - bin/rails db:schema:load

# Commands after code changes (e.g. after git pull)
sync:
  - bundle install
  - bin/rails db:migrate

# Named reset targets
reset:
  db:
    - bin/rails db:reset

# System-level updates
update:
  - sudo dnf update -y

# Port forwards (container_port:host_port)
ports:
  - 5432:15432
  - 3000:13000

# Environment variables
env:
  RAILS_ENV: development

# Long-running processes (managed as systemd user services)
daemons:
  rails: bin/rails server -b 0.0.0.0
  sidekiq:
    cmd: bundle exec sidekiq
    autostart: false

# Per-project agent overrides
agents:
  claude:
    mode: bedrock
    env:
      CLAUDE_CODE_USE_BEDROCK: "1"

# Enable nested Docker/Podman
docker: false

# Docker compose file to start on silo up
compose: ""
```

All fields are optional. The `setup` commands run as the `dev` user — use `sudo` for commands that need root.

## Global configuration

Silo uses sensible defaults. The config file (`~/.config/silo/config.yml`) only needs to contain your overrides:

```yaml
# Override the default agent command
agents:
  - name: claude
    cmd: claude --dangerously-skip-permissions
```

Run `silo config show` to see the full resolved configuration (defaults + overrides). Run `silo config edit` to open the config in your editor.

## Commands

### Environment

| Command | Description |
|---------|-------------|
| `silo up` | Start the environment (first run: provision; subsequent: resume) |
| `silo down` | Stop the container (preserves state) |
| `silo rm` | Remove the container and its data |
| `silo enter` | Open a shell inside the container |
| `silo run <cmd>` | Run a command inside the container |
| `silo cp <src> <dst>` | Copy files between host (`.`) and container (`:`) |
| `silo list` | List all silo containers |
| `silo status` | Show container state, config, and daemons |

### Agents

| Command | Description |
|---------|-------------|
| `silo ra` | Run the default agent interactively |
| `silo ra claude` | Run a specific agent |
| `silo ra claude "fix the tests"` | Run with an initial prompt |
| `silo ra claude ./prompt.md` | Run with a prompt from a file |

### Development workflow

| Command | Description |
|---------|-------------|
| `silo sync` | Run sync commands (after code changes) |
| `silo pull` | Git pull + sync |
| `silo reset <target>` | Run a named reset target |
| `silo update` | Run system-level update commands |

### Daemons

| Command | Description |
|---------|-------------|
| `silo start <name>` | Start a daemon |
| `silo stop <name>` | Stop a daemon |
| `silo restart <name>` | Restart a daemon |
| `silo logs [name]` | Tail daemon logs |

### Data management

| Command | Description |
|---------|-------------|
| `silo snapshot create [name]` | Take a snapshot |
| `silo snapshot list` | List snapshots |
| `silo snapshot restore <name>` | Restore a snapshot |
| `silo snapshot rm <name>` | Delete a snapshot |

### Configuration

| Command | Description |
|---------|-------------|
| `silo init` | Generate `.silo.yml` with AI |
| `silo init -m` | Generate `.silo.yml` with interactive wizard |
| `silo config show` | Print resolved global config |
| `silo config edit` | Open global config in `$EDITOR` |
| `silo config path` | Print config file path |
| `silo completion install` | Install shell completions |

## Agent credentials

Silo manages agent credentials separately from your host. On first `silo ra`, the agent will prompt you to log in. Credentials are stored in `~/.config/silo/agents/<name>/` and shared across all your containers.

Each agent has copy rules that define which files are synced:

- **Before launch**: credentials and settings are copied from the global agent dir into the container.
- **After exit**: updated credentials are copied back, so token refreshes propagate.

Files inside the agent home (e.g., `~/.claude/`) are mounted directly. Files outside (e.g., `~/.claude.json`) are synced via exec.

## Requirements

- [Incus](https://linuxcontainers.org/incus/) with a configured default profile (bridge network + storage pool)
- Linux (Incus system containers require a Linux host)

## License

MIT
