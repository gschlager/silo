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
┌──────────────────────────────────────────────────────────────┐
│ HOST                                                         │
│                                                              │
│  IDE ──────────┐                                             │
│  Git client ───┤── ~/project (real files)                    │
│  DB client ────┘         │                                   │
│                          │ bind mount                        │
│  localhost:15432 ────────┼──────────────┐                    │
│                          │ port forward │                    │
│           ┌──────────────┼─────────────┐│                    │
│           │ INCUS CONTAINER            ││                    │
│           │              ▼             ││                    │
│           │   /workspace/<project>     ││                    │
│           │                            ││                    │
│           │  Claude / Codex (agent)    ││                    │
│           │                            ││                    │
│           │  postgresql ── :5432 ──────┘│                    │
│           │  redis ─────── :6379        │                    │
│           │                             │                    │
│           │  ✗ No route to host         │                    │
│           │  ✓ Internet access          │                    │
│           └─────────────────────────────┘                    │
└──────────────────────────────────────────────────────────────┘
```

- The project directory is shared via bind mount — edits are instantly visible on both sides.
- Each project gets its own subdirectory under `/workspace/` (e.g. `/workspace/myapp`) so agents that key settings by path don't collide across projects.
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
# Runs with a login shell so the shell profile is sourced between commands.
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

# Port forwards (container_port:host_port, or just port for same on both sides)
# Ports can also be defined on daemons (see below).
ports:
  - 5432:15432   # PostgreSQL

# Environment variables
env:
  RAILS_ENV: development

# Additional bind mounts (host:container[:ro])
mounts:
  - ~/shared-cache:/home/dev/.cache/shared
  - ~/datasets:/data:ro

# Git configuration inside the container
git:
  user.name: Dev
  user.email: dev@example.com
  # Credential helper for https:// pushes (resolved on the host)
  credential:
    source: 1password
    ref: op://Private/github-token/token
    # Or: source: token, env: GITHUB_TOKEN
    # Or: source: token, value: ghp_xxx

# External tool credentials (resolved fresh on every session, not baked in)
tools:
  gh:                         # sets GH_TOKEN inside the container
    credential:
      source: 1password
      ref: op://Private/github-cli/token

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
- **`mode`** — Default authentication mode (e.g. `oauth`, `console`, `bedrock`)
- **`links`** — Rules for exposing files from the agent mode directory into the container via symlinks

Example:

```yaml
agents:
  - name: claude
    cmd: claude --dangerously-skip-permissions
    install: curl -fsSL https://claude.ai/install.sh | bash
    mode: oauth
    links:
      - source: .claude/          # trailing slash = directory
        target: ~/.claude/
      - source: .claude.json
        target: ~/.claude.json
```

Each agent mode gets its own host directory (`~/.config/silo/agents/<name>/<mode>/`). Silo mounts that directory at `/run/silo/<name>/` inside the container and creates a symlink from each `target` path to the matching `source` inside the mount. The agent reads and writes its config normally — changes land directly in the host mode directory, and token refreshes are immediately visible to any container sharing the same mode.

## Commands

### Environment

| Command                        | Description                                              |
|--------------------------------|----------------------------------------------------------|
| `silo up`                      | Start the environment (first run: provision; subsequent: resume) |
| `silo down`                    | Stop the container (preserves state)                     |
| `silo rm`                      | Remove the container and its data                        |
| `silo enter`                   | Open a shell inside the container                        |
| `silo run <cmd>`               | Run a command inside the container                       |
| `silo cp <src> <dst>`          | Copy files between host and container (`:` prefix)       |
| `silo list`                    | List all silo containers                                 |
| `silo status`                  | Show container state, config, and daemons                |

### Agents

| Command                        | Description                                              |
|--------------------------------|----------------------------------------------------------|
| `silo ra`                      | Run the default agent interactively                      |
| `silo ra claude`               | Run a specific agent                                     |
| `silo ra claude "fix the tests"` | Run with a prompt                                      |
| `silo ra claude --resume`      | Pass flags through to the agent                          |
| `silo ra --resume`             | Default agent with flags                                 |
| `silo mode`                    | Show current mode for all agents                         |
| `silo mode claude bedrock`     | Switch agent to a different mode                         |

### Development workflow

| Command                        | Description                                              |
|--------------------------------|----------------------------------------------------------|
| `silo sync`                    | Run sync commands (after code changes)                   |
| `silo pull`                    | Git pull + sync                                          |
| `silo reset <target>`          | Run a named reset target                                 |
| `silo update`                  | Run system-level update commands                         |

### Daemons

| Command                        | Description                                              |
|--------------------------------|----------------------------------------------------------|
| `silo start <name>`            | Start a daemon                                           |
| `silo stop <name>`             | Stop a daemon                                            |
| `silo restart [name]`          | Restart a daemon (or the container, if no name is given) |
| `silo logs [name]`             | Tail daemon logs                                         |

### Data management

| Command                        | Description                                              |
|--------------------------------|----------------------------------------------------------|
| `silo snapshot create [name]`  | Take a snapshot                                          |
| `silo snapshot list`           | List snapshots                                           |
| `silo snapshot restore <name>` | Restore a snapshot                                       |
| `silo snapshot rm <name>`      | Delete a snapshot                                        |

### Configuration

| Command                        | Description                                              |
|--------------------------------|----------------------------------------------------------|
| `silo init`                    | Generate `.silo.yml` with AI (default)                   |
| `silo init -m`                 | Generate `.silo.yml` with interactive wizard             |
| `silo init --agent codex`      | Use a specific agent for generation                      |
| `silo config show`             | Print resolved global config with syntax highlighting    |
| `silo config edit`             | Open global config in `$EDITOR`                          |
| `silo config path`             | Print config file path                                   |
| `silo completion install`      | Auto-install shell completions                           |

## Agent credentials

Silo manages agent credentials in its own directory (`~/.config/silo/agents/<name>/<mode>/`), separate from your host's agent config. This means agents inside containers can't access or modify your host's settings.

**First run**: The mode directory is seeded from your host's existing agent config (if any), then mounted into the container. The agent may still prompt you to log in on first launch; credentials are written directly into the mounted mode directory and are immediately visible to every container that uses the same mode.

**How syncing works**:

The agent mode directory is bind-mounted into the container at `/run/silo/<agent>/`, and symlinks are created from the paths the agent expects (e.g. `~/.claude/`, `~/.claude.json`) into that mount. There is no copy step — reads and writes happen directly against the host directory, so token refreshes and settings changes persist across sessions without any sync.

### Mode isolation

Each agent mode (e.g., `claude`, `console`, `bedrock`) gets its own data directory. Switching modes keeps history, settings, and credentials fully isolated — data from your personal Claude plan won't leak into a Bedrock session and vice versa.

```bash
silo mode                       # show current mode for all agents
silo mode claude bedrock        # switch claude to bedrock
silo mode claude claude         # switch back to personal plan
```

The mode can also be set as a default in `.silo.yml` or `.silo.local.yml` via `agents.<name>.mode`. The `silo mode` command overrides this per project.

**Directory structure**:

```
~/.config/silo/
├── config.yml                              # global overrides
├── agents/
│   └── claude/                             # shared across all containers
│       ├── oauth/                          # data for "oauth" mode
│       │   ├── .claude/                    # linked to ~/.claude/ in container
│       │   │   ├── .credentials.json
│       │   │   ├── settings.json
│       │   │   └── projects/
│       │   └── .claude.json                # linked to ~/.claude.json in container
│       └── bedrock/                        # data for "bedrock" mode (isolated)
│           ├── .claude/
│           └── .claude.json
└── containers/
    └── silo-myapp/
        └── mode.yml                        # per-project mode overrides (from silo mode)
```

Each mode directory is mounted into the container at `/run/silo/<agent>/`, and the paths listed under `links` are created as symlinks into that mount. Switching modes with `silo mode` swaps which mode directory is mounted — history, settings, and credentials from one mode never leak into another.

## Security model

Silo isolates AI agents in Linux system containers (Incus/LXC) with kernel-level namespaces. Each container has its own filesystem root, process tree, network stack, and user namespace.

| Threat                                     | Mitigated? | How                                                               |
|--------------------------------------------|------------|-------------------------------------------------------------------|
| Agent reads host credentials               | Yes        | Not in container — structurally absent                            |
| Agent modifies files outside project       | Yes        | Only project directory is bind-mounted                            |
| Agent runs destructive host commands       | Yes        | Separate process and user namespace                               |
| Agent corrupts project environment         | Yes        | Pre-session snapshots enable rollback                             |
| Agent accesses other projects' data        | Yes        | Each project gets its own container                               |
| Agent escapes via symlink/path traversal   | Yes        | Host paths don't exist in container namespace                     |
| Agent escalates via Docker socket          | Yes        | Host socket not mounted; nesting runs an isolated runtime         |
| Agent deletes or corrupts project files    | No         | Workspace is read-write by design; use git to recover             |
| Agent reads injected tool credentials      | No         | Passed as env vars by design; scope with per-project tokens (#7)  |
| Agent exfiltrates data via network         | No         | Containers have internet access by design                         |
| Agent exhausts host resources              | No         | No cgroup limits by default; configurable limits planned (#11)    |
| Agent accesses host/LAN services           | No         | Private network access not blocked by default; planned (#11)      |
| Malicious code in project dependencies     | Partially  | Contained blast radius, but not prevented                         |
| Agent exploits kernel vulnerability        | No         | Containers share the host kernel                                  |

`silo ra` takes a snapshot before each agent session, enabling rollback if an agent corrupts the environment. The 3 most recent pre-session snapshots are kept; older ones are cleaned up automatically. Manual snapshots via `silo snapshot create` are never affected.

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
