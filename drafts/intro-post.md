A while back I posted [a question](link-to-original-post) that had been nagging me:

> How is everyone making sure that their AI agents aren't reading any sensitive data like credentials or, even worse, executing something they shouldn't?

The trigger was a debugging session with Claude on translator-bot. Claude generated a small Ruby script — one of many — and at some point I stopped carefully evaluating every "allow this?" prompt and just clicked yes. That script ended up printing my credentials in the output. Nothing critical, easy to rotate, but it made something click: there is nothing technically stopping Claude from doing anything my user can do. Running `mothership`? Accessing customer data from a local migration? All fair game.

For Discourse work, `dv` is a solid answer — it gives us a constrained Docker environment purpose-built for this codebase. But what about everything else? Every other project where we point an AI agent at our code and hand it the keys?

That question is what led me to build **[silo](https://github.com/gschlager/silo)**.

### What it is

`silo` creates isolated development environments on **Linux** using [Incus](https://linuxcontainers.org/incus/) system containers. Each project gets its own container with its own filesystem, process tree, network stack, and user namespace — while your host-side workflow (IDE, git, DB tools) stays exactly the same via bind mounts and port forwarding.

Think of it as the same idea behind `dv`, but project-agnostic and built on Linux containers instead of Docker.

![silo architecture](https://raw.githubusercontent.com/gschlager/silo/main/docs/architecture.svg)

### Why Incus

Incus system containers are lightweight — they share the host kernel and start in under a second. There is no virtualization overhead, no Docker Desktop memory tax, no OrbStack needed. A Rails app inside a silo container performs essentially the same as running directly on the host.

On my workstation I routinely run multiple silo environments side by side without any noticeable slowdown.

### Security isolation

The core idea: agents can work freely inside the container, but structurally cannot reach the host.

| Threat | How silo handles it |
|---|---|
| Agent reads host credentials | Not in the container — structurally absent |
| Agent modifies files outside the project | Only the project directory is bind-mounted |
| Agent runs destructive host commands | Separate process and user namespace |
| Agent accesses other projects' data | Each project gets its own container |
| Agent escapes via symlink/path traversal | Host paths don't exist in the container namespace |
| Agent corrupts the dev environment | Pre-session snapshots enable rollback |

Before every `silo ra` (run agent) session, silo automatically takes a snapshot. If an agent corrupts your environment — broken packages, trashed config, bad migrations — you can roll back in seconds. The 3 most recent pre-session snapshots are kept automatically.

### Agent credential isolation

Agent credentials live in silo's own directory (`~/.config/silo/agents/`), completely separate from your host config. Agents inside containers cannot access or modify your host's settings.

Each agent mode (e.g. `claude` vs `bedrock`) gets fully isolated data — history, settings, and credentials don't leak between modes. Switch with `silo mode claude bedrock`.

### Getting started

```bash
# Install silo
go install github.com/gschlager/silo/cmd/silo@latest
silo completion install

# In any project directory
cd your-project
silo init          # AI-assisted config generation
silo up            # provision the environment
silo ra            # run your default agent
```

`silo init` spins up a temporary container, uses an AI agent to analyze your project, and generates a `.silo.yml` config. You can review and refine it interactively before committing.

### Discourse example

There is a ready-made config for Discourse in `examples/discourse/.silo.yml` — Fedora 43, PostgreSQL with pgvector, Valkey, Ruby via mise, Node.js, Playwright, MailHog, the works. If you want to try silo with Discourse as a starting point, that config should get you up and running.

### Workflow commands

Beyond the basics, a few things I find myself using constantly:

- **`silo sync`** / **`silo pull`** — run sync commands after code changes (bundle install, db:migrate, etc.)
- **`silo reset db`** — named reset targets for when you need a clean slate
- **`silo start`** / **`silo stop`** / **`silo logs`** — daemon management for long-running processes (rails server, ember-cli, etc.)
- **`silo snapshot create/restore`** — manual snapshots on top of the automatic pre-session ones
- **`silo enter`** — drop into a shell when you need to poke around

### Limitations

To be upfront about what silo does **not** do:

- **Linux only** — Incus system containers require a Linux host. No Mac or Windows support.
- **Agents can still delete project files** — the workspace is read-write by design; use git to recover.
- **No network restriction** — containers have internet access, so data exfiltration via network is not blocked.
- **Shared kernel** — containers share the host kernel, so a kernel exploit could escape.

### Current state

This is early and actively evolving. I have been using it daily for my own work and it has been solid, but I would love feedback — especially from anyone who has been thinking about the same isolation problems.

---

Questions welcome here! Happy to do a walkthrough or pair session for anyone who wants to try it out.
