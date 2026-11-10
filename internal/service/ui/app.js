// silo web UI. Everything that comes from a container (names, commands,
// logs) is put into the page with textContent, never as HTML.
"use strict";

const $ = (sel) => document.querySelector(sel);
const enc = encodeURIComponent;

// el("tag.class", {attr: value}, ...children): children are nodes or text.
function el(spec, attrs, ...children) {
  const [tag, ...classes] = spec.split(".");
  const node = document.createElement(tag || "div");
  if (classes.length) node.className = classes.join(" ");
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else node.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (method !== "GET") opts.headers["X-Silo-UI"] = "1";
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

let toastTimer;
function toast(msg, error) {
  const t = $("#toast");
  t.textContent = msg;
  t.className = "show" + (error ? " error" : "");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.className = ""), error ? 6000 : 2500);
}

// Actions still running, by path, so a button stays busy when the page is
// redrawn underneath it.
const pending = new Set();

// action is a button that POSTs to path. opts: cls, title, body, confirm,
// done (toast text from the result).
function action(label, path, opts = {}) {
  const key = path + (opts.body ? JSON.stringify(opts.body) : "");
  const busy = pending.has(key);
  return el("button" + (opts.cls ? "." + opts.cls : ""), {
    type: "button",
    title: opts.title,
    "data-key": key,
    disabled: busy,
    "aria-busy": busy ? "true" : null,
    onclick: async (ev) => {
      if (opts.confirm && !confirm(opts.confirm)) return;
      const b = ev.currentTarget;
      pending.add(key);
      b.disabled = true;
      b.setAttribute("aria-busy", "true");
      try {
        const res = await api("POST", path, opts.body);
        toast(opts.done ? opts.done(res) : label + ": done");
      } catch (e) {
        toast(e.message, true);
      } finally {
        pending.delete(key);
        refresh();
      }
    },
  }, label);
}

function ago(iso) {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 90) return "just now";
  if (s < 5400) return Math.round(s / 60) + " min ago";
  if (s < 172800) return Math.round(s / 3600) + " h ago";
  return Math.round(s / 86400) + " days ago";
}

function bytes(n) {
  if (!n) return "";
  const gib = n / 1024 ** 3;
  return gib >= 1 ? gib.toFixed(1) + " GiB" : Math.round(n / 1024 ** 2) + " MiB";
}

const short = (name) => name.replace(/^silo-/, "");
const plural = (n, one, many) => n + " " + (n === 1 ? one : many);

function copyButton(label, text, title) {
  return el("button.link", {
    type: "button",
    title,
    onclick: () => navigator.clipboard.writeText(text)
      .then(() => toast("Copied: " + text), () => toast("Couldn't copy to the clipboard", true)),
  }, label);
}

// Top bar, notices and the service panel.

function renderHeader(o) {
  const cs = o.containers;
  const running = cs.filter((c) => c.state === "running").length;
  $("#summary").replaceChildren(...(cs.length
    ? [el("b", {}, running), " running · ", el("b", {}, cs.length - running), " stopped"]
    : []));
  const ok = o.service.incus;
  $("#health").className = "health" + (ok ? "" : " bad");
  $("#health").replaceChildren(el("span.dot." + (ok ? "running" : "failed")), ok ? "Incus connected" : "Incus not connected");

  const notices = [];
  if (!ok) {
    notices.push(el("p.notice", {}, el("strong", {}, "The silo service can't reach Incus. "),
      "Check that Incus is running, then look at ", el("code", {}, "silo service status"), "."));
  }
  if (o.proxy.http_port && o.proxy.http_port !== 80) {
    notices.push(el("p.notice", {}, el("strong", {}, "The proxy runs on ports " + o.proxy.http_port + " and " + o.proxy.https_port + ". "),
      "Run ", el("code", {}, "silo setup"), " so silo may use ports 80 and 443 and the URLs work without a port."));
  }
  const withUpdates = cs.filter((c) => c.config_updates.some((u) => !u.careful));
  const outdated = cs.filter((c) => c.outdated && !c.tmp);
  if (withUpdates.length || outdated.length) {
    const parts = [];
    if (withUpdates.length) parts.push(plural(withUpdates.length, "project has config updates", "projects have config updates"));
    if (outdated.length) parts.push(plural(outdated.length, "container was built by an older silo", "containers were built by an older silo"));
    notices.push(el("div.notice.info", {},
      el("p", {}, el("strong", {}, "Updates: "), parts.join(" · ") + "."),
      withUpdates.length ? action("Apply all config updates", "/api/config-updates", {
        cls: "small",
        title: "applies what silo config update --yes would in " + withUpdates.map((c) => c.label).join(", "),
        done: (r) => "Applied " + plural(r.applied, "change", "changes") + " in " + plural(r.projects, "project", "projects") +
          (r.left ? "; " + r.left + " need silo config update in the terminal" : ""),
      }) : null));
  }
  $("#notices").replaceChildren(...notices);
}

function renderService(o) {
  const s = o.service;
  const row = (term, ...value) => [el("dt", {}, term), el("dd", {}, ...value)];
  const proxy = o.proxy.http_port
    ? el("span" + (o.proxy.http_port !== 80 ? ".warn" : ""), {}, "ports " + o.proxy.http_port + " / " + o.proxy.https_port)
    : el("span.off", {}, "not listening");
  $("#service").replaceChildren(
    ...row("Version", el("span.mono", {}, "v" + s.version.replace(/^v/, ""))),
    ...row("Proxy", proxy),
    ...row("Idle stop", o.idle_stop ? "after " + o.idle_stop : el("span.off", {}, "off")),
    ...row("Commit signing", o.signing
      ? el("span.mono", { title: "the key containers sign commits with: " + o.signing }, o.signing)
      : el("span.off", {}, "off")),
    ...row("Secrets", s.cached_secrets ? s.cached_secrets + " cached" : el("span.off", {}, "none cached"),
      s.cached_secrets ? action("Forget", "/api/secrets/forget", {
        cls: "small", title: "drop the cached 1Password values; silo asks again when it needs them",
        done: (r) => "Forgot " + r.count + " secrets",
      }) : null),
  );
}

function renderBases(o) {
  if (!o.bases.length) return $("#bases").replaceChildren(el("p.hint", {}, "No base templates yet. The first silo up builds one."));
  $("#bases").replaceChildren(el("ul.bases", {}, o.bases.map((b) => el("li", {},
    el("div", {},
      el("div.mono", {}, b.image.replace(/^images:/, "")),
      b.refreshing ? el("div.when.busy", {}, "Rebuilding…") : el("div.when", { title: b.built_at }, "built " + ago(b.built_at))),
    b.refreshing ? null : action("Rebuild", "/api/bases/refresh", {
      cls: "small", body: { image: b.image }, done: (r) => (r.started ? "Rebuilding " : "Already rebuilding ") + b.image,
    }),
  ))));
}

// Containers.

function urlLinks(c) {
  return el("div.links", {}, c.urls.map((u) => {
    const href = u.https_url || u.url;
    return el("a", { href, target: "_blank", rel: "noopener", title: "port " + u.container_port + " in the container" },
      href.replace(/^https?:\/\//, "").replace(/\/$/, ""));
  }));
}

function portList(c) {
  return el("ul.ports", {}, c.ports.map((p) => el("li", { title: "port " + p.container + " in the container" },
    el("b", {}, "localhost:" + p.host), " → " + p.container, p.name ? "  " + p.name : "")));
}

function daemonList(c) {
  const base = "/api/containers/" + enc(c.name) + "/daemons/";
  return el("ul.daemons", {}, c.daemons.map((d) => {
    const path = base + enc(d.name) + "/";
    const up = d.state === "running";
    return el("li.daemon." + d.state, {},
      el("div.daemon-name", {}, el("span.dot." + d.state, { title: d.state }), d.name,
        d.state === "failed" ? el("span.state.failed", {}, "failed") : null,
        d.autostart ? el("span.auto", { title: "starts with the container" }, "auto") : null),
      el("code.cmd", { title: d.cmd }, d.cmd),
      el("div.seg", {},
        up ? action("Restart", path + "restart", { cls: "small" }) : action("Start", path + "start", { cls: "small" }),
        up ? action("Stop", path + "stop", { cls: "small" }) : null,
        el("button.small", { type: "button", onclick: () => showLogs(c.name, d) }, "Logs")),
    );
  }));
}

// Open snapshot lists, by container: the snapshots last loaded and the count
// they were loaded at, so a refresh keeps the list open without reloading it.
const openSnaps = new Map();

function snapshotsFoot(c) {
  const open = openSnaps.get(c.name);
  const list = el("div.snap-list", { hidden: !open });
  const n = c.snapshots;
  const label = n === 0 ? "No snapshots" : n === 1 ? "1 snapshot" : n + " snapshots";
  const toggle = n
    ? el("button.link", {
        type: "button",
        "aria-expanded": open ? "true" : "false",
        "data-key": "snaps:" + c.name,
        onclick: (ev) => {
          if (openSnaps.has(c.name)) openSnaps.delete(c.name);
          else openSnaps.set(c.name, { count: -1, snaps: null });
          list.hidden = !openSnaps.has(c.name);
          ev.currentTarget.setAttribute("aria-expanded", String(!list.hidden));
          if (!list.hidden) loadSnapshots(c, list);
        },
      }, label + (open ? " ▴" : " ▾"))
    : el("span", {}, label);
  if (open) {
    if (open.snaps && open.count === n) drawSnapshots(c, list, open.snaps);
    else loadSnapshots(c, list);
  }
  return el("div.box-foot", {}, toggle,
    action("Take snapshot", "/api/containers/" + enc(c.name) + "/snapshots", { cls: "small", done: (r) => "Took snapshot " + r.name }),
    list);
}

async function loadSnapshots(c, list) {
  if (!list.firstChild) list.append(el("p.hint", {}, "Loading…"));
  try {
    const snaps = await api("GET", "/api/containers/" + enc(c.name) + "/snapshots");
    const open = openSnaps.get(c.name);
    if (open) Object.assign(open, { count: c.snapshots, snaps });
    drawSnapshots(c, list, snaps);
  } catch (e) {
    list.replaceChildren(el("p.warn-line", {}, e.message));
  }
}

function drawSnapshots(c, list, snaps) {
  if (!snaps.length) return list.replaceChildren(el("p.hint", {}, "No snapshots."));
  list.replaceChildren(el("table", {}, el("tbody", {}, snaps.slice().reverse().map((s) => el("tr", {},
    el("td.mono", {}, s.Name),
    el("td.when", {}, s.CreatedAt),
    el("td.act", {}, action("Restore", "/api/containers/" + enc(c.name) + "/snapshots/" + enc(s.Name) + "/restore", {
      cls: "small.danger",
      confirm: "Restore " + c.label + " to " + s.Name + "? Changes in the container since then are lost (the mounted project directory isn't affected).",
      done: () => "Restored " + c.label + " to " + s.Name,
    })),
  )))));
}

// updatesBlock shows the config updates for a container's project, and
// whether an older silo built the container.
function updatesBlock(c) {
  const rows = [];
  const ups = c.config_updates;
  if (ups.length) {
    const auto = ups.filter((u) => !u.careful);
    rows.push(el("div.update", {},
      el("div.update-text", {},
        el("strong", {}, plural(ups.length, "config update", "config updates")), " ",
        el("span.what", {}, ups.map((u) => u.title).join(" · "))),
      el("div.btns", {},
        el("button.small", { type: "button", onclick: () => showConfigUpdates(c) }, "Show changes"),
        auto.length ? action(auto.length === ups.length ? "Apply" : "Apply " + auto.length, "/api/containers/" + enc(c.name) + "/config-updates", {
          cls: "small",
          done: (r) => "Applied " + plural(r.applied, "change", "changes") + " to " + c.label +
            (r.left ? "; " + r.left + " need silo config update in the terminal" : ""),
        }) : null)));
  }
  if (c.outdated && !c.tmp && c.project_dir) {
    rows.push(el("div.update", {},
      el("div.update-text", {},
        el("strong", {}, "Built by an older silo"), " ",
        el("span.what", {}, (c.built_by ? "(" + c.built_by + "). " : "(before silo recorded it). ") +
          "Rebuild it to get the current setup. That removes everything in the container outside the project directory.")),
      el("div.btns", {},
        copyButton("Copy rebuild command", "cd " + JSON.stringify(c.project_dir) + " && silo rm && silo up",
          "silo rm asks before it removes the container"))));
  }
  return rows.length ? el("div.updates", {}, rows) : null;
}

function renderContainer(c) {
  const path = "/api/containers/" + enc(c.name) + "/";
  const running = c.state === "running";
  const stats = running
    ? [bytes(c.memory_bytes), c.ipv4, c.last_active ? "active " + ago(c.last_active) : ""]
    : [c.last_active ? "last used " + ago(c.last_active) : ""];

  const head = el("div.box-head", {},
    el("div.ident", {},
      el("span.dot." + c.state, { title: c.state }),
      el("h3", {}, c.label),
      c.tmp ? el("span.tag", { title: "a throwaway container from silo tmp" }, "tmp") : null,
      running || c.state === "stopped" ? null : el("span.state." + c.state, {}, c.state)),
    el("div.stats", {}, stats.filter(Boolean).map((s) => el("span", {}, s))),
    el("div.btns", {},
      running ? action("Restart", path + "restart", { done: () => "Restarted " + c.label }) : null,
      running ? action("Stop", path + "stop", { cls: "danger", done: () => "Stopped " + c.label }) : null,
      running ? null : action("Start", path + "start", { cls: "primary", done: () => "Started " + c.label })));

  const where = c.project_dir ? el("div.path", {},
    el("code", { title: "project directory" }, c.project_dir),
    copyButton("Copy silo ra command", "cd " + JSON.stringify(c.project_dir) + " && silo ra",
      "copies a command that runs the default agent in this container")) : null;
  const problem = c.config_error ? el("p.warn-line", {}, c.config_error) : null;

  let body;
  if (running) {
    const reach = c.urls.length || c.ports.length ? el("section", {},
      c.urls.length ? [el("h4", {}, "Open"), urlLinks(c)] : null,
      c.ports.length ? [el("h4", {}, "Ports"), portList(c)] : null) : null;
    const daemons = c.daemons.length ? el("section", {}, el("h4", {}, "Daemons"), daemonList(c)) : null;
    if (reach || daemons) body = el("div.box-body" + (reach && daemons ? "" : ".single"), {}, reach, daemons);
  } else {
    const bits = [
      c.urls.length ? urlLinks(c) : null,
      c.urls.length ? el("span.hint", {}, "opening a URL starts it") : null,
      c.daemons.length ? el("span", {}, "daemons: " + c.daemons.map((d) => d.name).join(", ")) : null,
    ].filter(Boolean);
    if (bits.length) body = el("div.quiet", {}, bits);
  }

  return el("article.box." + (running ? "running" : "stopped"), { "aria-label": c.label },
    el("div.box-top", {}, head, where, problem), updatesBlock(c), body, snapshotsFoot(c));
}

function renderContainers(o) {
  if (!o.containers.length) {
    return $("#containers").replaceChildren(el("p.empty", {}, o.service.incus
      ? ["No silo containers yet. Run ", el("code", {}, "silo up"), " in a project."]
      : "The silo service can't reach Incus, so there are no containers to show."));
  }
  const running = o.containers.filter((c) => c.state === "running");
  const other = o.containers.filter((c) => c.state !== "running");
  const group = (title, cs) => cs.length
    ? [el("h2.group-title", {}, title, el("span.n", {}, cs.length)), ...cs.map(renderContainer)]
    : [];
  $("#containers").replaceChildren(...group("Running", running), ...group("Stopped", other));
}

async function refresh() {
  try {
    const o = await api("GET", "/api/overview");
    // Keep keyboard focus on the same control across the redraw.
    const focused = document.activeElement && document.activeElement.dataset ? document.activeElement.dataset.key : null;
    renderHeader(o);
    renderService(o);
    renderBases(o);
    renderContainers(o);
    if (focused) {
      const again = document.querySelector('[data-key="' + CSS.escape(focused) + '"]');
      if (again) again.focus({ preventScroll: true });
    }
  } catch (e) {
    toast("Can't load the overview: " + e.message, true);
  }
}

// Dialog: daemon logs, or a project's config updates.

function openDialog(title, sub, actions, body) {
  $("#dialog-title").textContent = title;
  $("#dialog-sub").textContent = sub;
  $("#dialog-sub").title = sub;
  $("#dialog-actions").replaceChildren(...actions,
    el("button", { type: "button", onclick: () => $("#dialog").close() }, "Close"));
  if (body) $("#dialog-body").replaceChildren(body);
  if (!$("#dialog").open) $("#dialog").showModal();
}

function showLogs(container, daemon) {
  const refreshBtn = el("button", { type: "button", onclick: load }, "Refresh");
  openDialog(daemon.name + " · " + short(container), daemon.cmd, [refreshBtn], el("p.hint", {}, "Loading…"));
  async function load() {
    refreshBtn.disabled = true;
    refreshBtn.setAttribute("aria-busy", "true");
    try {
      const r = await api("GET", "/api/containers/" + enc(container) + "/daemons/" + enc(daemon.name) + "/logs");
      const pre = el("pre", { tabindex: "0" }, r.text || "(no output yet)");
      $("#dialog-body").replaceChildren(pre);
      pre.scrollTop = pre.scrollHeight;
    } catch (e) {
      $("#dialog-body").replaceChildren(el("p.warn-line", {}, e.message));
    } finally {
      refreshBtn.disabled = false;
      refreshBtn.removeAttribute("aria-busy");
    }
  }
  load();
}

function diffView(f) {
  return el("div.diff", {},
    el("div.diff-path", {}, f.path),
    el("pre", {}, f.lines.map((l) => l.op === ""
      ? el("span.gap", {}, "  …")
      : el("span" + (l.op === "+" ? ".add" : l.op === "-" ? ".del" : ""), {}, l.op + " " + l.text))));
}

async function showConfigUpdates(c) {
  const path = "/api/containers/" + enc(c.name) + "/config-updates";
  openDialog("Config updates · " + c.label, c.project_dir, [], el("p.hint", {}, "Loading…"));
  try {
    const ups = await api("GET", path);
    if (!ups.length) return $("#dialog-body").replaceChildren(el("p.hint", {}, "The config is up to date."));
    const auto = ups.filter((u) => !u.careful).length;
    const apply = auto ? el("button.primary", {
      type: "button",
      onclick: async (ev) => {
        const b = ev.currentTarget;
        b.disabled = true;
        b.setAttribute("aria-busy", "true");
        try {
          const r = await api("POST", path);
          toast("Applied " + plural(r.applied, "change", "changes") + " to " + c.label +
            (r.left ? "; " + r.left + " need silo config update in the terminal" : ""));
          $("#dialog").close();
        } catch (e) {
          toast(e.message, true);
          b.disabled = false;
          b.removeAttribute("aria-busy");
        }
        refresh();
      },
    }, auto === ups.length ? "Apply all" : "Apply " + auto) : null;
    openDialog("Config updates · " + c.label, c.project_dir, apply ? [apply] : [], el("div.diffs", {}, ups.map((u) => el("section.suggestion", {},
      el("h3", {}, u.title),
      u.detail ? el("p", {}, u.detail) : null,
      u.careful ? el("p.careful", {}, "This removes something silo can't interpret, so the web UI leaves it alone. Run silo config update in the project to review it.") : null,
      u.files.map(diffView)))));
  } catch (e) {
    $("#dialog-body").replaceChildren(el("p.warn-line", {}, e.message));
  }
}

$("#dialog").addEventListener("close", () => $("#dialog-body").replaceChildren());
$("#dialog").addEventListener("click", (e) => { if (e.target === $("#dialog")) $("#dialog").close(); });

// Activity: events are shown in the list, and they trigger a refresh.

// describe turns an event into [text, bad?].
function describe(e) {
  const c = e.container ? short(e.container) : "";
  switch (e.type) {
    case "container-started": return [c + " started"];
    case "container-stopped": return [c + " stopped"];
    case "container-idle-stopped": return [c + " stopped after being idle"];
    case "container-deleted": return [c + " was deleted"];
    case "daemons-started": return ["Daemons started in " + c];
    case "daemons-failed": return ["Daemons failed in " + c, true];
    case "open-requested": return [c + " asked to open a URL"];
    case "opened": return ["Opened a URL from " + c];
    case "commit-signed": return ["Signed a commit in " + c];
    case "signing-refused": return ["Refused to sign in " + c, true];
    case "secrets-forgotten": return ["Forgot the cached secrets"];
    case "config-updated": return ["Updated the config of " + c];
    case "notification": return [c ? "Notification from " + c : "Notification"];
  }
  return [e.type.replaceAll("-", " ") + (c ? " · " + c : "")];
}

let refreshTimer;
function onEvent(e) {
  const list = $("#events");
  if (list.querySelector(".none")) list.replaceChildren();
  const t = new Date(e.time);
  const [what, bad] = describe(e);
  list.prepend(el("li" + (bad ? ".bad" : ""), {},
    el("time", { datetime: e.time, title: t.toLocaleString() }, t.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })),
    el("span.what", {}, what),
    e.message ? el("span.msg", {}, e.message) : null));
  while (list.children.length > 60) list.lastElementChild.remove();
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(refresh, 400);
}
const events = new EventSource("/api/events");
events.onmessage = (m) => { try { onEvent(JSON.parse(m.data)); } catch (_) {} };

refresh();
setInterval(() => { if (!document.hidden && !$("#dialog").open) refresh(); }, 5000);
document.addEventListener("visibilitychange", () => { if (!document.hidden) refresh(); });
