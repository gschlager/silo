# silo

Secure isolated local environments for AI coding agents.

`silo` creates development environments on **Linux** using [Incus](https://linuxcontainers.org/incus/) system containers, providing full network and service isolation while keeping your existing workflow (host-side IDE, git client, DB tools) intact via bind mounts and port forwarding.

## Why

AI coding agents run with your full user permissions. They can read any file, connect to any local service, and access any credential on your machine. `silo` gives each project its own isolated container where agents can work freely without risking your host system.

## Quick start

```bash
# Install silo from COPR (Fedora 45 or newer). The RPM pulls in Incus
# and enables its units.
sudo dnf copr enable gschlager/silo
sudo dnf install silo

# Prepare the host: Incus access, first-time init, firewall, memory cap
silo setup

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

On other distributions, [install Incus](https://linuxcontainers.org/incus/docs/main/installing/) first, then take the binary from a [GitHub release](https://github.com/gschlager/silo/releases) or run `go install github.com/gschlager/silo/cmd/silo@latest`.

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

### Base templates

New containers are copied from a base template instead of being built from the bare image. A template is a stopped container per image (e.g. `silo-base-fedora-45`) where `default_setup`, the login shell, the dev user and the globally enabled agents are already set up. A new project container only runs the project-specific steps: mounts, ports, git config, env, `setup:` and daemons.

silo builds the template the first time it's needed and rebuilds it when one of those inputs changes (image, `default_setup`, user, shell, or an agent's `deps`/`install`). Agents a project enables on top of the global ones are installed per container. Templates don't show up in `silo list`; use `silo base` to see them.

To keep it fresh, a template older than a week is still used, but silo rebuilds it in the background for the next container (log: `~/.config/silo/base-refresh.log`). The [silo service](#the-silo-service) runs these rebuilds (one per image at a time, shown in `silo service status`) and also checks every few hours for templates that got old while no `silo up` ran. Without the service, `silo up` starts the rebuild as a detached process. A rebuild starts from the current upstream image and installs the latest agents, so there's nothing to update in place. `silo up --fresh` rebuilds it first and waits, for when you need the latest right now.

### Throwaway containers

`silo tmp` starts a container from the base template, runs your default agent in it and removes the container when the agent exits:

```sh
silo tmp                                                   # current directory, mounted
silo tmp markdown-it/linkify-it                            # cloned inside the container
silo tmp https://github.com/markdown-it/linkify-it/issues/126   # ... and the agent starts on the issue
silo tmp ../other --shell                                  # a shell instead of an agent
silo tmp owner/repo -- "fix the failing test"              # arguments after -- go to the agent
```

A directory doesn't need a `.silo.yml`, but if it has one it's used, except for `ports:` (they'd clash with the project's own container). A cloned repository only gets the global config. Its credentials come from the repo's entry in `secrets.yml` (e.g. `linkify-it:`), or else from a `tmp:` entry, so you can keep one GitHub token there for throwaway work.

A clone that has uncommitted or unpushed changes isn't removed without asking. Pass `--keep` to keep the container anyway; kept ones are listed by `silo tmp ls`, reopened with `silo tmp enter <name>` and removed with `silo tmp rm <name>` (or `--all`).

## Project configuration

Create a `.silo.yml` in your project root (or run `silo init` to generate one):

```yaml
# silo project configuration
# https://github.com/gschlager/silo#project-configuration

# Base image (default: fedora/45)
image: fedora/45

# Built-in presets install common stacks so you don't copy-paste the same
# setup steps between projects. Preset commands run before your own setup:
# below. jruby/truffleruby are just entries in versions — silo routes MRI
# through rv and the rest through ruby-install. Run `silo config show` to see
# the resolved setup.
use:
  ruby:
    versions: [3.3, 3.4, "4.0", jruby]   # jruby/truffleruby are just versions
    default: "3.4"                        # version to pin (optional)
  node:

# Commands run once on first provisioning (as dev user with sudo).
# All lines run in one login shell session with `set -e`, so PATH/env
# changes (export, eval, cd) carry over from one command to the next.
# To persist activation for future silo enter/ra/daemon sessions, append a
# POSIX-sh line to ~/.silo/env.sh — it is sourced by every login shell (bash
# and zsh), by daemons, and via BASH_ENV by every non-interactive bash
# (agent tool calls, scripts, git hooks),
# e.g. `echo 'eval "$(mise activate bash)"' >> ~/.silo/env.sh`.
# (The ruby preset already wires rv this way.)
setup:
  - sudo dnf install -y postgresql16-server redis
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
# Ports can also be defined on daemons (see below). A port declared both here
# and on a daemon is forwarded once; the entry in this section wins, so it
# controls the host-side mapping. It's still listed under that daemon in
# `silo status`.
ports:
  - 5432:15432   # PostgreSQL
  # Forwards can be named — the name shows up in `silo status` and is used for
  # the Incus device name (handy when a forward fails to start). `http: true`
  # marks a web server: it gets http(s)://<project>.localhost (see ".localhost
  # URLs") and `silo status --json` reports the URL:
  - name: web
    port: 9292:9292
    http: true

# Environment variables
env:
  RAILS_ENV: development

# Git configuration inside the container
git:
  user.name: Dev
  user.email: dev@example.com

# GitHub PATs and other per-project secrets go in
# ~/.config/silo/secrets.yml (see "Secrets" below), not here.

# Long-running processes (managed as systemd user services).
# Daemons always run in the project root (/workspace/<project>).
daemons:
  rails:
    cmd: bin/rails server -b 0.0.0.0
    ports: ["3000:13000"]
  sidekiq:
    cmd: bundle exec sidekiq
    after: rails              # systemd dependency (After + Requires)
    autostart: false
  worker:
    cmd: my-runner
    env:                      # scoped to this daemon only
      API_TOKEN: op://Employee/worker/credential

# Per-project agent overrides. For a mode's env (Bedrock/Vertex credentials,
# model ARNs), prefer defining it once globally under agents[].modes so every
# project inherits it — see "Modes and env". A project env: still layers on top.
agents:
  claude:
    mode: bedrock
    env:
      ANTHROPIC_DEFAULT_OPUS_MODEL: "arn:aws:bedrock:us-west-2:..."

# Disable an agent for this project
# agents:
#   codex:
#     enabled: false

# Enable container nesting (Docker, Podman, etc.)
nesting: false
```

All fields are optional. Setup commands run as the `dev` user with a login shell — use `sudo` for commands that need root (dnf, systemctl, etc.).

### Keeping .silo.yml up to date

`silo config update` looks for things in `.silo.yml` and `.silo.local.yml` that are out of date with your silo version, shows each change as a diff and applies it when you agree:

- an older release of the default image (`fedora/44` while the default is `fedora/45`)
- `mounts:`, which project files can't declare anymore: they move to `project_mounts` in the global config
- `git.credential` and `tools.<name>.credential`: they move to [secrets.yml](#secrets) as the same environment variables, which keeps them out of the repo and out of the agent's reach. silo refuses to start a project that still has them, like it does for `mounts:`
- port forwards on a usual web server port (3000, 4200, 5173, 8080, ...) without `http: true`, so they get a [`.localhost` name](#the-silo-service)
- keys silo doesn't know (anymore): `docker: true` becomes `nesting: true`, a typo like `deamons:` becomes `daemons:`, and anything else is offered for removal (default no)

Comments and formatting stay as they are; only the affected lines change. `--dry-run` only shows the suggestions, `--yes` applies them without asking (except removing unknown keys). `silo up` prints one line when there's something to update.

### Local overrides

Create a `.silo.local.yml` alongside `.silo.yml` to adjust settings per machine without modifying the shared config. Add `.silo.local.yml` to your project's `.gitignore`.

The local file extends the keyed blocks rather than replacing them, so it only needs to list what it adds or changes:

- `use`, `daemons`, `env`, `agents`, `tools`: local entries are added to the shared ones. An entry with the same name replaces the shared entry (a `use:` preset keeps its position but takes the local parameters).
- `ports`: local forwards are appended. A local entry for a container port already forwarded in `.silo.yml` replaces that entry, so you can change the host port per machine.
- `image`, `setup`, `sync`, `reset`, `update`, `git`, `nesting`: set in the local file, they replace the shared value entirely.

```yaml
# .silo.local.yml — add a preset and a daemon, keep everything from .silo.yml
use:
  valkey:
daemons:
  mailhog: mailhog
```

The project directory is writable from its container, so `.silo.local.yml` is not a trusted host-policy file: it cannot declare `mounts` either. Put host mounts in the global config. For secrets, prefer the central [Secrets](#secrets) file — it keeps every project's PAT outside the project.

## Global configuration

Silo uses sensible defaults for everything. The config file (`~/.config/silo/config.yml`) only needs to contain your overrides — missing fields use the built-in defaults automatically. New features added in updates work immediately without changing your config.

The default login shell is `bash` (always present, including on images without zsh). Set `shell: zsh` to use zsh instead — silo installs it if the image doesn't ship it. Tool activations (rv, mise, …) live in a shell-neutral `~/.silo/env.sh`, so they work for either shell, for daemons, and — via `BASH_ENV` — for non-interactive bash shells like agent tool calls and git hooks.

```yaml
# Base image used when a project doesn't set image: (default: fedora/45)
default_image: fedora/45

# Packages installed in every container before project setup runs (they end
# up in the base template). This replaces the built-in list below, so copy it
# all when you only want to add a package.
# Fail-fast: the first command that exits non-zero aborts provisioning, so
# anything only some projects want (or that needs a token) belongs in a
# preset instead — see "Presets".
default_setup:
  - dnf install -y git curl wget make gcc which jq socat ripgrep fd-find tree gh ncurses

# Agent silo ra launches when you don't name one (default: first defined, claude)
default_agent: claude

# Login shell for every container: bash (default) or zsh
shell: bash

# Container user agents run as (default: dev)
user: dev

# Host env vars passed into enter/run/ra sessions. This is for terminal state,
# not for getting values to a daemon — daemons never see pass_env (see Daemons).
pass_env: [TERM, COLORTERM, LANG, LC_ALL]

# Desktop notifications from `silo-notify` in the container (default: off).
# Not used for containers in herdr, which alerts on its own.
notifications: true

# Add containers to herdr when its hc is installed (default: on, see "herdr")
herdr: false

# Stop containers nobody used for this long (default: 4h, 0 turns it off).
# See "Idle stop".
idle_stop: 8h

# Host bind mounts (host:container[:ro]). Mounts are host-owned security policy,
# so they are accepted only here, never from a repository's .silo*.yml files.
mounts:
  - ~/shared-cache:/home/dev/.cache/shared

# Prefer read-only mounts for data that the container does not need to modify.
#  - ~/datasets:/data:ro

# Host-owned mounts for one project only. Get the exact readable project key
# from `silo config show`; the suffix distinguishes same-named directories.
project_mounts:
  myapp-a1b2c3d4e5f6a7b8:
    - ~/datasets:/data:ro

# Git settings applied in every container. Merged key by key, so a project that
# needs a different address overrides just user.email and keeps the name.
git:
  user.name: Dev
  user.email: dev@example.com
  # Sign commits with an SSH key from the host's agent (1Password), see
  # "Commit signing". The key is the public key itself.
  # gpg.format: ssh
  # user.signingkey: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... me@example.com
  # commit.gpgsign: "true"

# Override an agent's launch command, install step, mode, links, or per-mode env
agents:
  - name: claude
    cmd: claude --dangerously-skip-permissions
    modes:                        # env injected when the mode is active
      bedrock:
        env:
          CLAUDE_CODE_USE_BEDROCK: "1"
          AWS_ACCESS_KEY_ID: op://Employee/aws-bedrock/access-key-id
```

All fields are optional — list only what you want to override. Per-project `.silo.yml` values take precedence over global values, except host mounts, which are deliberately accepted only from this host-owned file. Use `mounts` for every container and `project_mounts` for a single readable project key. Repository files are writable from inside their container and therefore cannot grant themselves access to additional host paths.

Run `silo config edit` to open the global config in your editor, or `silo config path` to print its location. Run `silo config show` (from a project directory) to see the fully resolved configuration for that project — global + project merged, presets expanded into setup, and which secrets apply (by reference; tokens are never printed).

### Presets

A preset is a named bundle a project opts into with `use:`. Silo ships built-in
presets for language runtimes (`ruby`, `node`), and you can define your own in
the global config:

```yaml
# ~/.config/silo/config.yml
presets:
  segno:
    setup:
      - >-
        printf 'header = "Authorization: Bearer %s"\n' "$SEGNO_INSTALL_TOKEN" |
        curl -fsSL --config - https://segno.example/runner -o ~/.local/bin/segno-runner
    secrets:                          # resolved on the host, never on disk
      SEGNO_INSTALL_TOKEN: op://Employee/segno-install/credential
    env:                              # plain settings, written into the container
      SEGNO_HOME: /opt/segno
    daemons:
      segno-claude:
        cmd: segno-runner claude
        env:
          SEGNO_TOKEN: op://Employee/segno-claude/credential
      segno-codex:
        cmd: segno-runner codex
        env:
          SEGNO_TOKEN: op://Employee/segno-codex/credential
```

```yaml
# .silo.yml — in the three projects that want it, and nowhere else
use:
  segno:
```

A user preset can contribute `setup`, `secrets`, `env` and `daemons`. Definition
is global, activation is per project — which is why this exists instead of a
global `daemons:` or a longer `default_setup`: a sidecar wanted by three
projects shouldn't start in all twenty, and an installer that needs a token
shouldn't break provisioning for projects that don't have one.

**`secrets:` vs `env:`** — these are not interchangeable, and picking the wrong
one fails in a confusing way:

| | `secrets:` | `env:` |
|---|---|---|
| `op://` references | resolved on the host | **never resolved** — used as a literal |
| Where the value lives | exec environment only | `/etc/environment.d` and `~/.silo/env.sh`, on disk |
| Reaches | setup, `enter`/`run`/`ra`, daemons | every shell in the container |
| Use for | tokens, credentials | `SEGNO_HOME`, `RAILS_ENV` |

A preset's `secrets:` merge into the same set as
[`secrets.yml`](#secrets), so they behave exactly like a project secret —
including the reserved `github` name — but only in projects that opted into the
preset. That is the point: the reference is written once, not repeated in every
project's `secrets.yml` block.

Putting an `op://` reference in an `env:` block is rejected at config load with
a message pointing here, rather than silently becoming a literal that fails
later as an unauthorized request halfway through provisioning.

Precedence and ordering:

- Presets expand in `use:` declaration order, and their setup commands run
  before the project's own `setup:`. List a preset after the runtime it needs.
- The project's `env:`, `daemons:` and `secrets.yml` entries win over a preset's
  on a key collision, so a project can override a single variable, swap one
  daemon's command, or point a secret at a different item.
- A built-in preset wins over a user preset of the same name, so a built-in
  added in a later release can't be silently shadowed by an old config entry.

Built-in presets take typed parameters (`ruby: {versions: [3.4.1]}`); user
presets are plain config and take none.

### Secrets

Per-project secrets live in one central file, `~/.config/silo/secrets.yml`, keyed by the readable name assigned in `projects.yml`. On the first `silo up`, silo appends the exact key to use as a commented stub:

```yaml
# ~/.config/silo/secrets.yml
migrations-tooling:
  github: op://Employee/migrations-pat/token   # reserved key: wires git + gh
converters:
  github: op://Employee/converters-pat/token
  AWS_BEARER_TOKEN_BEDROCK: op://Employee/bedrock/key   # plain env var
```

- Each value is a **1Password reference** (`op://vault/item/field`) resolved on the host via the `op` CLI, or a literal. References are just pointers, not secrets, so this file mainly holds `op://` paths.
- The reserved **`github`** key exports `GITHUB_TOKEN` and `GH_TOKEN` and wires the git credential helper for `github.com`. Every other key becomes a plain environment variable of that name.
- Secrets are resolved at session and setup time and passed as environment variables — never baked into the container or written to disk.
- The [silo service](#the-silo-service) keeps the values it got from `op` in memory, so not every silo command (and every herdr reconnect) needs `op` and a 1Password approval again. The cache is cleared when the screen locks, before suspend, after 12 hours at the latest, and with `silo service forget`. Run that after rotating a PAT in 1Password, so the next session picks up the new one. The cache never reaches a container; see [The silo service](#the-silo-service) for why it doesn't weaken anything. Values for `op://` references in a project's `.silo.yml` (per-daemon `env:`, a project's agent `env:`) are cached for that project only, so a project can't pick up a value another project caused to be cached.
- A [preset](#presets) can carry its own `secrets:` block, resolved the same way but scoped to the projects that opted into the preset with `use:`. Handy when several projects need the same reference and you'd rather not repeat it here.
- Secrets reach `silo enter`/`silo run`, project setup, and [daemons](#daemons). Daemons get them because silo injects them into the systemd user manager when it starts the daemon (see Daemons) — still only in memory, never on disk.

If you used the short-lived silo version that exposed path hashes in secrets,
run `silo config migrate-secrets` once from each affected project. It atomically
renames the hash-suffixed key back to the registry-assigned readable key,
preserving comments and refusing to overwrite a key that already exists. The
hash-suffixed key remains a read fallback until it is migrated, so upgrading
does not interrupt existing credentials. A same-basename project that receives
a suffixed registry name uses that distinct name for its secrets too.

#### Sharing secrets between projects

The file is plain YAML, so anchors and merge keys work — useful when several
projects point at the same 1Password items:

```yaml
# ~/.config/silo/secrets.yml
.shared: &shared
  AWS_BEARER_TOKEN_BEDROCK: op://Employee/bedrock/key

discourse-a1b2c3d4e5f6a7b8:
  <<: *shared
  github: op://Employee/discourse-pat/token

converters-f6e7d8c9b0a1b2c3:
  <<: *shared
```

A key starting with `.` is never treated as a project: silo only ever looks up
the name and hash derived from the project directory, and those can't begin with a dot.
So the anchor block is inert — it exists to be merged into the real entries.

This is deliberately opt-in per project rather than a `common:` block that
applies everywhere. A key that every container gets is a key every container can
read, which is rarely what you want for a PAT.

### Global gitignore

`~/.config/silo/gitignore` is a single global ignore file applied in every container. silo writes it into each container as `~/.config/git/ignore` (git's default per-user excludes), so it ignores the listed patterns in **all** repositories. It is seeded with sensible defaults (`.silo.yml`, `.silo.local.yml`, `.idea/`, `.vscode/`, `.claude/`, `.codex/`, `.DS_Store`) on first run.

Edit the one file and the change applies to every container on its next `silo up` or `silo enter` — no reprovision. Empty the file to disable.

### Agent configuration

Each agent has:

- **`cmd`** — How to launch the agent (default: agent name). silo also defines a shell function with the agent's name in the container, so starting the agent by name anywhere there (a shell, [herdr](#herdr)) runs this command
- **`deps`** — System dependencies installed as root before the agent
- **`install`** — Install command run as the dev user
- **`mode`** — Default authentication mode (e.g. `oauth`, `console`, `bedrock`)
- **`modes`** — Per-mode env injected when that mode is active (see [Modes and env](#modes-and-env))
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

Each agent mode gets its own host directory (`~/.config/silo/agents/<name>/<mode>/`). Silo mounts that directory at `/var/lib/silo/<name>/` inside the container and creates a symlink from each `target` path to the matching `source` inside the mount. The agent reads and writes its config normally — changes land directly in the host mode directory, and token refreshes are immediately visible to any container sharing the same mode.

#### Modes and env

Switching a mode with `silo mode claude bedrock` picks the mode's data directory, but a hosted provider like Bedrock also needs env vars (`CLAUDE_CODE_USE_BEDROCK`, credentials, the model ARNs). Define those once per mode under `modes:` in the **global** config and every project inherits them — switching to the mode carries its env automatically:

```yaml
# ~/.config/silo/config.yml
agents:
  - name: claude
    modes:
      bedrock:
        env:
          CLAUDE_CODE_USE_BEDROCK: "1"
          AWS_REGION: us-west-2
          ANTHROPIC_DEFAULT_SONNET_MODEL: "arn:aws:bedrock:us-west-2:..."
          ANTHROPIC_DEFAULT_OPUS_MODEL:   "arn:aws:bedrock:us-west-2:..."
          ANTHROPIC_DEFAULT_HAIKU_MODEL:  "arn:aws:bedrock:us-west-2:..."
          AWS_ACCESS_KEY_ID:     op://Employee/aws-bedrock/access-key-id
          AWS_SECRET_ACCESS_KEY: op://Employee/aws-bedrock/secret-access-key
```

Values are literals or `op://` [1Password references](#secrets) — the `op://` ones are read on the host at launch time and passed to the agent as env, never written to disk. Only the active mode's block is injected, so `bedrock` and `vertex` can each carry their own credentials without colliding. A per-project agent `env:` (in `.silo.yml`) still layers on top and wins on key collisions — handy for overriding a single model ARN in one project. `silo status` lists the env var names the active mode will inject (names only, never values).

The env reaches the agent through `silo ra`. Run `silo down && silo up` after switching a mode so the new data directory is mounted.

## Commands

### Environment

| Command                        | Description                                              |
|--------------------------------|----------------------------------------------------------|
| `silo up`                      | Start the environment (first run: provision; subsequent: resume) |
| `silo up --keep`               | Keep the container if provisioning fails, for inspection |
| `silo up --fresh`              | Rebuild the base template first (latest packages/agents) |
| `silo down`                    | Stop the container (preserves state)                     |
| `silo rm`                      | Remove the container and its data                        |
| `silo enter`                   | Open a shell inside the container                        |
| `silo run <cmd>`               | Run a command inside the container                       |
| `silo cp <src> <dst>`          | Copy files between host and container (`:` prefix)       |
| `silo list`                    | List all silo containers                                 |
| `silo status`                  | Show container state, config, and daemons                |
| `silo status --json`           | Container, daemon and port state as JSON (for scripts)   |
| `silo tmp [dir\|repo\|url]`     | Throwaway container with an agent, removed on exit       |
| `silo tmp ls` / `enter` / `rm` | List, reopen or remove kept tmp containers               |
| `silo base`                    | List the base templates new containers are copied from   |
| `silo base rebuild`            | Rebuild the template for this project's image            |
| `silo base rm [name...]`       | Remove base templates (all without arguments)            |

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
| `silo daemon list`             | List configured daemons and their state                  |
| `silo daemon start <name>`     | Start a daemon                                           |
| `silo daemon stop <name>`      | Stop a daemon                                            |
| `silo daemon restart <name>`   | Restart a daemon                                         |
| `silo daemon logs [name]`      | Tail daemon logs (all daemons if no name given)          |

silo starts daemons itself — the units are installed but not enabled for boot, so silo can inject the environment first. `silo up` starts the `autostart` daemons. When the container is started some other way (a lazy start by `silo ra` or `silo enter`, `incus start`, the [.localhost proxy](#localhost-urls) waking it up), the [silo service](#the-silo-service) notices and does the same in the background. Without the service running, only `silo up` starts them. Each start re-resolves the project's `env:` values and secrets and injects them into the systemd user manager, so editing `env:` or a `secrets.yml` entry takes effect on the next `silo up` (or `silo daemon restart`) without recreating the container. Secrets stay in the manager's memory and are never written to disk.

**Working directory.** Every daemon runs in the project root — `/workspace/<project>`, the same directory `silo enter` drops you in. This is a guarantee, not a default: a daemon that shells out to `git` or resolves paths relative to the repo root can rely on it.

**Environment.** Daemons see the project's `env:` block and every resolved secret, because silo injects those into the systemd user manager before starting them. They do **not** see `pass_env` — that list is host terminal state (`TERM`, `LANG`, …) and only reaches `silo enter`, `silo run` and `silo ra` sessions. A daemon that needs a value from the host environment should get it from `env:` or `secrets.yml` instead.

### Per-daemon environment

The user manager's environment is shared by every unit, so a project-wide `env:` value reaches all daemons under the same name. When two daemons read the same variable name but need different values, give each one its own `env:` block:

```yaml
daemons:
  segno-claude:
    cmd: segno-runner claude
    env:
      SEGNO_TOKEN: op://Employee/segno-claude/credential
  segno-codex:
    cmd: segno-runner codex
    env:
      SEGNO_TOKEN: op://Employee/segno-codex/credential
```

Values are literals or `op://` references, resolved on the host at start time — the same rules as `env:` and `agents[].modes`. A per-daemon key overrides the project-wide `env:` for that daemon only.

Do not write the shell equivalent by hand:

```yaml
cmd: env SEGNO_TOKEN="$SEGNO_TOKEN_CODEX" segno-runner codex   # don't
```

`/proc/<pid>/cmdline` is world-readable, so a token passed as a command argument is visible to every process in the container. With `env:`, silo injects the value into the user manager under an internal name and the unit renames it at start time, so the value only ever reaches the daemon through its environment — never through `argv`, and never through the unit file on disk. `silo config show` lists the variable names each daemon injects (names only, never values).

Note that this scopes *naming*, not *access*: the manager environment is shared, so a daemon can still read another daemon's project-wide secrets. Per-daemon `env:` solves the collision, not isolation.

### Snapshots

| Command                        | Description                                              |
|--------------------------------|----------------------------------------------------------|
| `silo snapshot create [name]`  | Take a snapshot                                          |
| `silo snapshot list`           | List snapshots                                           |
| `silo snapshot restore <name>` | Restore a snapshot                                       |
| `silo snapshot rm <name>`      | Delete a snapshot                                        |

### Configuration

| Command                        | Description                                              |
|--------------------------------|----------------------------------------------------------|
| `silo setup`                   | Prepare the host: Incus access, init, firewall, memory cap (default 80%) |
| `silo setup --memory-max 24GiB`| Same, with a specific combined memory cap                |
| `silo service status`          | Show the state of the [silo service](#the-silo-service)  |
| `silo init`                    | Generate `.silo.yml` with AI (default)                   |
| `silo init -m`                 | Generate `.silo.yml` with interactive wizard             |
| `silo init --agent codex`      | Use a specific agent for generation                      |
| `silo config show`             | Show the resolved config for the current project (presets expanded) |
| `silo config edit`             | Open global config in `$EDITOR`                          |
| `silo config path`             | Print config file path                                   |
| `silo config update`           | Update `.silo.yml` to the current silo, change by change (see [Keeping .silo.yml up to date](#keeping-siloyml-up-to-date)) |
| `silo config migrate-secrets`  | Rename this project's legacy secrets key once            |
| `silo completion install`      | Auto-install shell completions                           |

## Agent credentials

Silo manages agent credentials in its own directory (`~/.config/silo/agents/<name>/<mode>/`), separate from your host's agent config. This means agents inside containers can't access or modify your host's settings.

**First run**: The mode directory starts empty — silo never copies your host's agent config or credentials into a container. The agent prompts you to log in on first launch; the resulting credentials are written into the mounted mode directory and are then immediately visible to every container that uses the same mode, so you only log in once per mode.

**How syncing works**:

The agent mode directory is bind-mounted into the container at `/var/lib/silo/<agent>/`, and symlinks are created from the paths the agent expects (e.g. `~/.claude/`, `~/.claude.json`) into that mount. There is no copy step — reads and writes happen directly against the host directory, so token refreshes and settings changes persist across sessions without any sync.

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
├── projects.yml                            # path hashes → stable container names
├── secrets.yml                             # per-project secrets (PATs, etc.)
├── gitignore                               # global gitignore for every container
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
        ├── mode.yml                        # per-project mode overrides (from silo mode)
        └── shell                           # shell the container was provisioned with
```

Container names stay readable and backward-compatible (`silo-myapp`). Silo
records the canonical-path identity in host-owned `projects.yml`; only a second
project with the same basename receives a hash suffix. The registry stores the
hash, not the host path.

Each mode directory is mounted into the container at `/var/lib/silo/<agent>/`, and the paths listed under `links` are created as symlinks into that mount. Switching modes with `silo mode` swaps which mode directory is mounted — history, settings, and credentials from one mode never leak into another.

## herdr

[herdr](https://herdr.dev) shows agents from several machines in one app. silo
detects herdr on its own: when herdr's `hc` is on the host's `PATH`, `silo up`
adds the container as a herdr machine named like the container without the
`silo-` prefix (`myapp`) and opens a shell in a space for the project, so it
shows up in the sidebar.
Start agents from that space. Without herdr on the host, silo doesn't touch
herdr at all and `silo ra` runs the agent directly.

`silo ra` runs the agent as a terminal of that herdr-core too, in a new tab of
the project's space, and attaches to it. So agents you start from the CLI show
up in herdr as well, and keep running when you detach with `ctrl+\` or close
the terminal; reattach from herdr or with `hc attach`. Sessions started before
the container was a herdr machine don't show up; continue them with
`silo ra --resume`.

herdr-core reaches the container through `silo connect` instead of SSH, so the
container needs no sshd. When adding, hc copies its own herdr-core to
`~/.local/bin` in the container and starts a core there, which owns the agent
terminals. The core runs in a login shell as the agent user, with the
project's secrets and every enabled agent's mode env, just like a `silo ra`
session. `pass_env` is not used, because the terminals belong to herdr. The
core is long-running, so after changing secrets or `silo mode`, restart it with
`hc machine update silo-myapp`.

`silo connect` never starts a stopped container. herdr-core reconnects to every
machine it knows, so that would boot all containers whenever herdr starts. A
stopped container shows as unreachable in herdr until the next `silo up`.
`silo rm` removes the machine from herdr again.

The host's core dials into the container, never the other way round. herdr-core
only lets the container answer host terminals that started work in it, so an
agent in the container cannot run anything on the host through herdr. Agents
started from herdr don't get `silo ra`'s pre-session snapshot.

herdr alerts you itself when an agent needs input, so silo doesn't send its own
desktop notifications (`notifications: true`) for a container in herdr.

To turn the integration off even with herdr installed, set `herdr: false` in
`~/.config/silo/config.yml`. silo then leaves herdr alone on `silo up` and
`silo ra`; drop a machine it added before with `hc machine rm silo-myapp`.

## The silo service

Some things don't fit into a single silo command, so silo also runs as a small background service per user: `silo.service`, a systemd user service started through `silo.socket`. The RPM enables it for every user from their next login on, and `silo setup` installs and starts it right away (also for `go install` builds).

The CLI talks to it over `$XDG_RUNTIME_DIR/silo/silo.sock`, which only your user can open; the service also checks the uid of every connection. That socket is never mounted into a container. Everything that uses the service keeps working without it, minus the feature itself.

When silo and the service come from different builds (after an upgrade, or a rebuilt binary), the CLI asks the service to stop and systemd starts the new binary on the next connection.

| Command                | Description                                         |
|------------------------|-----------------------------------------------------|
| `silo service status`  | Show whether the service runs and what it does, and the web UI's URL |
| `silo service events`  | Follow the service's events (`--json` for tools)    |
| `silo service forget`  | Forget all cached 1Password secrets                  |
| `silo service restart` | Restart the service                                  |

Logs go to the journal: `journalctl --user -u silo`.

Tools can follow what the service sees with `silo service events --json` (or `GET /v1/events` on the socket): one JSON object per line for containers starting and stopping, idle stops, daemons it started, `silo-notify` messages, URLs opened from containers, commit signatures, base template refreshes and forgotten secrets. herdr, a status bar or a script can react to "agent needs input" without polling.

### Container sockets

Each container gets its own directory from the host, mounted at `/var/lib/silo/host`, where the service listens on the container's sockets. The service knows which container asks by the socket the request comes in on, and a container can only ask for the few things listed here. `silo up` adds the mount to containers created before the service existed.

- **Notifications**: `silo-notify "message"` shows a desktop notification on the host, when `notifications: true` is set in the global config and herdr isn't handling the container. Hook it into your agent, e.g. Claude Code's `Notification` hook. It works for every agent in the container, also those started through herdr or `silo connect`, not only while a `silo ra` runs.
- **Opening URLs**: `silo-open URL` opens an http(s) URL in the host's browser. `BROWSER` points to it and `xdg-open` in the container calls it, so `gh pr view --web` and agent logins just work. Every URL needs a click on the notification first ("myapp wants to open …"); otherwise an agent could open whatever it wants in a browser where you're logged in everywhere. A container can only have one URL waiting at a time. A `localhost` URL goes to where that port is forwarded on the host, or is refused when the port isn't forwarded.

- **Commit signing**: `ssh-agent.sock`, see below.

### Commit signing

To sign commits in containers with an SSH key from 1Password (or any other SSH agent on the host), set it up with the usual git settings in the global config:

```yaml
# ~/.config/silo/config.yml
git:
  gpg.format: ssh
  user.signingkey: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... me@example.com
  commit.gpgsign: "true"
```

On `silo up`, silo points git's `gpg.ssh.program` to `silo-ssh-sign`, which runs `ssh-keygen` against the container's `ssh-agent.sock`. The service passes requests on to the host's agent (`~/.1password/agent.sock`, otherwise `SSH_AUTH_SOCK`), but only:

- it lists the configured key and no other key
- it signs only git signatures (the `SSHSIG` format with the namespace `git`, which is what `git commit -S` asks for). An SSH login challenge can't look like that, so the key can't be used to log in anywhere from a container.
- it refuses everything else: adding or removing keys, locking, extensions

Only the global config counts for the key; a project's `.silo.yml` can't pick another one. `SSH_AUTH_SOCK` is only set for `ssh-keygen`, so `ssh` in the container doesn't see the socket. 1Password sees the silo service as the application, so you approve once per unlock, not per container. Every signature is logged: `journalctl --user -u silo`.

What can't be avoided: while 1Password's approval lasts, an agent in a container can get any commit signed. "Verified" on GitHub then means "made by me or in one of my silo containers". Use a separate key that you add on GitHub only as a **signing** key, never as an authentication key.

### .localhost URLs

Every `http: true` port forward gets a name under `.localhost`, served by the service's proxy:

- the first one is `http://<project>.localhost` (and `https://`)
- the others get the forward's name (or container port) appended: `http://<project>-admin.localhost`

Browsers resolve every name under `.localhost` to the loopback address on their own, and so does systemd-resolved, so there's no DNS to set up. The proxy keeps the `.localhost` name in the `Host` header (Rails, Vite and others allow `.localhost` by default) and supports WebSockets and streaming, so HMR works. For every `http: true` forward, `silo up` adds an Incus proxy device that listens on a Unix socket in `$XDG_RUNTIME_DIR/silo/http/` (only yours) and connects to the port on the container's own localhost, so dev servers that only listen on 127.0.0.1 work too. The proxy only connects to those sockets, never to a host port from `.silo.yml`, so a project can't point its name at another service on your machine. Run `silo up` once in existing projects to get the sockets.

Visiting a stopped container's URL starts it (with its autostart daemons) and waits for the port, so a bookmark is enough to bring a project back. Only a top-level visit by your own user does that; an image or a `fetch()` from some web page doesn't wake containers. `silo status` and `silo status --json` show the URLs, and a `localhost` URL opened with `silo-open` goes to the `.localhost` name.

**HTTPS**: no public CA issues certificates for `.localhost`, so silo has its own local CA. `silo setup` creates it and adds it to the system trust store and to Chrome's and Firefox's NSS databases (with `certutil` from `nss-tools`). The CA is name-constrained: it can only issue certificates for `localhost` and names under it, only for TLS servers, and browsers enforce that. Even if its key (`~/.config/silo/ca`, readable only by you) leaked, it couldn't be used to fake any other site. Plain `http://<project>.localhost` already counts as a secure context in browsers, so service workers, `crypto.subtle` and secure cookies work there too.

**Ports**: the proxy listens on 80 and 443 on loopback only. The service runs as your user, so the silo binary has `cap_net_bind_service` for that (set by the RPM, and by `silo setup` for other installs; a rebuilt binary loses it). Without it, the proxy uses 7080 and 7443 and the URLs include the port.

### Web UI

`https://silo.localhost` (or `http://`) shows all silo containers: their state, memory, `.localhost` URLs, port forwards and daemons, with live updates from the event stream. From there you can start, stop and restart containers and daemons, read a daemon's recent logs, take and restore snapshots, rebuild base templates and forget the cached secrets. It also shows what `silo config update` suggests for each project, with the diff, and applies it for one project or all of them (changes that remove keys silo doesn't know stay in the terminal). A container that an older silo built gets a note with the command to rebuild it. A project called `silo` gets `silo-<name>.localhost` names instead, since `silo.localhost` is the UI.

It only answers processes running as you: the proxy ports are on loopback, where other users of the machine could reach them too, so the service looks up the owner of every client socket in `/proc/net/tcp`. Other web pages can't use it either: API requests must come from the UI's own origin, changes also need a header that other origins can't send, and the apps in your containers (`<project>.localhost`) count as other origins. The UI can't be framed, and everything that comes from a container is shown as text.

On a machine with other users: loopback ports are shared, so another user could take ports 80/443 (or 7080/7443) while your service isn't running and pretend to be silo. Plain HTTP can't tell. HTTPS can, because only your service has the key of your local CA, so use `https://silo.localhost` there.

### Idle stop

A running container holds memory under the shared cap even when nobody uses it. The service stops containers that have been idle for 4 hours (`idle_stop:` in the global config, `0` turns it off) and shows a notification. A container counts as used while it has a terminal (any open pty: `silo ra` and `silo enter` sessions, herdr's shells and agents, tmux), while it uses more than 5% of a CPU core, and for a while after a request through the proxy. A stopped container comes back on `silo ra`, `silo enter`, `silo up` or its `.localhost` URL, with its autostart daemons.

### Secrets cache

Every `op read` is a separate process, and 1Password asks you to approve the op CLI per terminal session. herdr reconnecting to a container, a `silo ra` in a new tab or a lazy start each asked again. Now the first silo command that reads a secret asks `op` (you approve as before), stores the value in the service's memory, and later commands get it from there.

The cache is cleared when a session locks its screen or the machine suspends (silo watches logind), after 12 hours at the latest (the longest an op authorization lasts), with `silo service forget`, and when the service stops. Nothing is written to disk.

This doesn't weaken anything: containers can't reach the cache, and a process on the host running as you can already read every secret silo has injected into a running container, without any prompt (`incus-admin` is root-equivalent, so it can `incus exec` into any container). The cache doesn't hand anything new to an attacker there.

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
| Agent reads injected tool credentials      | No         | Passed as env vars by design; give each project its own token in the central secrets file |
| Agent exfiltrates data via network         | No         | Containers have internet access by design                         |
| Agent exhausts host resources              | Partially  | Run `silo setup` to cap the combined memory of all containers (default 80% of host RAM); off until then |
| Agent accesses host/LAN services           | No         | Private network access not blocked by default; planned (#11)      |
| Malicious code in project dependencies     | Partially  | Contained blast radius, but not prevented                         |
| Agent exploits kernel vulnerability        | No         | Containers share the host kernel                                  |

`silo ra` takes a snapshot before each agent session, enabling rollback if an agent corrupts the environment. The 3 most recent pre-session snapshots are kept; older ones are cleaned up automatically. Manual snapshots via `silo snapshot create` are never affected.

### Host setup

Run `silo setup` once on a new machine (it's safe to run again, every step checks first):

- **Incus access**: adds you to the `incus-admin` group. Log out and back in afterwards.
- **First-time init**: if Incus has no storage pool yet, it runs `incus admin init` with a NAT bridge (`incusbr0`), a default storage pool (a btrfs subvolume when `/var/lib/incus` is on btrfs, a plain directory otherwise) and a default profile using both. An existing Incus setup is never changed.
- **Firewall**: when firewalld is running, puts `incusbr0` into the trusted zone so containers get DHCP, DNS and internet access.
- **Memory cap**: see below.
- **Ports 80 and 443**: gives the silo binary `cap_net_bind_service` for the [.localhost proxy](#localhost-urls).
- **Local CA**: creates silo's CA for `https://<project>.localhost` and trusts it in the system and the browsers, see [.localhost URLs](#localhost-urls).
- **silo service**: installs, enables and starts it, see [The silo service](#the-silo-service).

The RPM also installs `silo-incus-docker-fix.service`. Docker's firewall rules drop forwarded traffic, which cuts Incus containers off from the network; this unit lets the `incusbr0` traffic through whenever Docker starts. On hosts without Docker it does nothing.

### Capping container memory

By default Incus puts no memory limit on a container, so a runaway process or a `/tmp`/`/dev/shm` fill (tmpfs lives in RAM) could drive the host out of memory. Run `silo setup` once to fix this. It sets `lxc.cgroup.relative = 1` on the default profile so containers land inside the `incus.service` cgroup, then sets `MemoryMax` on that unit — which caps the *combined* memory of all silo containers (default 80% of host RAM, override with `--memory-max`). Containers overcommit within the shared pool, but the host stays protected: hitting the cap OOM-kills a process inside a container, not on the host. Already-running containers move under the cap on their next `silo down && silo up`.

## Building

```bash
make build      # build with version from git
make install    # install to $GOPATH/bin
make vet        # run go vet
```

Pushing a `v*` tag makes a release: [GoReleaser](https://goreleaser.com/) publishes the tarballs as a GitHub release, and COPR builds the RPM for the same tag. An RPM built from an untagged commit gets a snapshot version like `0.6.0^20261110git1a2b3c4`.

## Requirements

- [Incus](https://linuxcontainers.org/incus/) 6.x or 7.x with a configured default profile (bridge network + storage pool). Silo uses the current client library against Incus's backward-compatible REST API.
- Linux (Incus system containers require a Linux host)

## License

MIT
