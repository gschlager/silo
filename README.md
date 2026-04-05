# silo

Secure isolated local environments for AI coding agents.

`silo` creates development environments on **Linux** using [Incus](https://linuxcontainers.org/incus/) system containers, providing full network and service isolation while keeping your existing workflow (host-side IDE, git client, DB tools) intact via bind mounts and port forwarding.

## Why

AI coding agents run with your full user permissions. They can read any file, connect to any local service, and access any credential on your machine. `silo` gives each project its own isolated container where agents can work freely without risking your host system.

## Quick start

```bash
# Install Incus (if not already installed)
# See https://linuxcontainers.org/incus/docs/main/installing/

# Install silo
go install github.com/gschlager/silo/cmd/silo@latest

# Set up shell completions
silo completion install

# Generate a project config with AI
cd your-project
silo init

# Start the environment
silo up

# Run an AI agent
silo ra
```

`silo init` spins up a temporary container, uses an AI agent to analyze your project, and generates a `.silo.yml` configuration file. It shows you the result with syntax highlighting and lets you refine it interactively. `silo up` provisions the environment, and `silo ra` launches your default agent inside it.

## How it works

```
┌─────────────────────────────────────────────────────────┐
│ HOST                                                    │
│                                                         │
│  IDE ──────────┐                                        │
│  Git client ───┤── ~/project (real files)               │
│  DB client ────┘         │                              │
│                          │ bind mount                   │
│  localhost:15432 ────────┼──────────────┐               │
│                          │ port forward │               │
│           ┌──────────────┼─────────────┐│               │
│           │ INCUS CONTAINER            ││               │
│           │              ▼             ││               │
│           │    /workspace (shared)     ││               │
│           │                            ││               │
│           │  Claude / Codex (agent)    ││               │
│           │                            ││               │
│           │  postgresql ── :5432 ──────┘│               │
│           │  redis ─────── :6379        │               │
│           │                             │               │
│           │  ✗ No route to host         │               │
│           │  ✓ Internet access          │               │
│           └─────────────────────────────┘               │
└─────────────────────────────────────────────────────────┘
```

- The project directory is shared via bind mount — edits are instantly visible on both sides.
- Services run inside the container, isolated from host services.
- Port forwarding exposes container services to host tools.
- The container has no route to the host's localhost ports.

## Project configuration

Create a `.silo.yml` in your project root (or run `silo init` to generate one):

```yaml
# silo project configuration
# https://github.com/gschlager/silo#project-configuration

# Base image (default: fedora/43)
image: fedora/43

# Commands run once on first provisioning (as dev user with sudo).
# Runs with a login shell so ~/.profile is sourced between commands.
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
# Ports can also be defined on daemons (see below).
ports:
  - 5432:15432   # PostgreSQL

# Environment variables
env:
  RAILS_ENV: development

# Long-running processes (managed as systemd user services)
daemons:
  rails:
    cmd: bin/rails server -b 0.0.0.0
    ports: ["3000:13000"]
  sidekiq:
    cmd: bundle exec sidekiq
    after: rails              # systemd dependency (After + Requires)
    autostart: false

# Per-project agent overrides
agents:
  claude:
    mode: bedrock
    env:
      CLAUDE_CODE_USE_BEDROCK: "1"

# Disable an agent for this project
# agents:
#   codex:
#     enabled: false

# Enable container nesting (Docker, Podman, etc.)
nesting: false
```

All fields are optional. Setup commands run as the `dev` user with a login shell — use `sudo` for commands that need root (dnf, systemctl, etc.).

### Local overrides

Create a `.silo.local.yml` alongside `.silo.yml` to override settings per machine without modifying the shared config. Non-zero values in the local file replace the base values. Add `.silo.local.yml` to your project's `.gitignore`.

## Global configuration

Silo uses sensible defaults for everything. The config file (`~/.config/silo/config.yml`) only needs to contain your overrides — missing fields use the built-in defaults automatically. New features added in updates work immediately without changing your config.

```yaml
# Override the default agent command
agents:
  - name: claude
    cmd: claude --dangerously-skip-permissions
```

Run `silo config show` to see the full resolved configuration (defaults + your overrides) with syntax highlighting. Run `silo config edit` to open the config in your editor.

### Agent configuration

Each agent has:

- **`cmd`** — How to launch the agent (default: agent name)
- **`deps`** — System dependencies installed as root before the agent
- **`install`** — Install command run as the dev user
- **`copy`** — Rules for syncing files between silo's agent dir and the container
- **`set`** — Values to deep-merge into config files inside the container

Example with copy rules and set:

```yaml
agents:
  - name: claude
    copy:
      - file: .credentials.json
        target: ~/.claude/.credentials.json
      - file: claude.json
        target: ~/.claude.json
        keys: [oauthAccount, userID, hasCompletedOnboarding]
    set:
      ~/.claude.json:
        projects:
          /workspace:
            hasTrustDialogAccepted: true
```

Copy rules with `keys` sync only the listed top-level JSON keys, preserving everything else. The `set` field deep-merges values into files before the agent launches.

## Commands

### Environment

| Command | Description |
|---------|-------------|
| `silo up` | Start the environment (first run: provision; subsequent: resume) |
| `silo down` | Stop the container (preserves state) |
| `silo rm` | Remove the container and its data |
| `silo enter` | Open a shell inside the container |
| `silo run <cmd>` | Run a command inside the container |
| `silo cp <src> <dst>` | Copy files between host and container (`:` prefix) |
| `silo list` | List all silo containers |
| `silo status` | Show container state, config, and daemons |

### Agents

| Command | Description |
|---------|-------------|
| `silo ra` | Run the default agent interactively |
| `silo ra claude` | Run a specific agent |
| `silo ra claude "fix the tests"` | Run with an initial prompt |
| `silo ra claude ./prompt.md` | Run with a prompt from a file |
| `silo mode` | Show current mode for all agents |
| `silo mode claude bedrock` | Switch agent to a different mode |

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
| `silo init` | Generate `.silo.yml` with AI (default) |
| `silo init -m` | Generate `.silo.yml` with interactive wizard |
| `silo init --agent codex` | Use a specific agent for generation |
| `silo config show` | Print resolved global config with syntax highlighting |
| `silo config edit` | Open global config in `$EDITOR` |
| `silo config path` | Print config file path |
| `silo completion install` | Auto-install shell completions |

## Agent credentials

Silo manages agent credentials in its own directory (`~/.config/silo/agents/<name>/`), separate from your host's agent config. This means agents inside containers can't access or modify your host's settings.

**First run**: The agent prompts you to log in. Credentials are saved to silo's agent dir and shared across all containers automatically.

**How syncing works**:

1. Before `silo ra`: credentials are copied from the global agent dir into the container
2. Agent runs interactively
3. After exit: updated credentials (token refreshes) are copied back to the global dir

Files inside the agent home (e.g., `~/.claude/.credentials.json`) are handled via an Incus disk mount. Files outside the agent home (e.g., `~/.claude.json`) are synced into the container via exec.

**Directory structure**:

```
~/.config/silo/
├── config.yml                              # global overrides
├── agents/
│   └── claude/                             # shared credentials & settings
│       ├── .credentials.json
│       └── settings.json
└── containers/
    └── silo-myapp/
        └── agents/
            └── claude/
                ├── home/                   # mounted as /home/dev/.claude/
                │   ├── .credentials.json
                │   ├── settings.json
                │   ├── projects/           # per-project agent data
                │   └── auto-memory/
                └── files/                  # out-of-home files (exec-synced)
                    └── claude.json         # → /home/dev/.claude.json
```

## Building

```bash
make build      # build with version from git
make install    # install to $GOPATH/bin
make vet        # run go vet
```

Releases are built with [GoReleaser](https://goreleaser.com/) and published as GitHub releases with RPM packages.

## Requirements

- [Incus](https://linuxcontainers.org/incus/) with a configured default profile (bridge network + storage pool)
- Linux (Incus system containers require a Linux host)

## License

MIT
