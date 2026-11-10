// silo web UI. Everything that comes from a container (names, commands,
// logs) is put into the page with textContent, never as HTML.
"use strict";

const $ = (sel) => document.querySelector(sel);

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

// action runs a POST from a button, disabling it meanwhile.
function action(label, path, opts = {}) {
  return el("button" + (opts.cls ? "." + opts.cls : ""), {
    type: "button",
    title: opts.title,
    onclick: async (ev) => {
      if (opts.confirm && !confirm(opts.confirm)) return;
      const b = ev.currentTarget;
      b.disabled = true;
      try {
        const res = await api("POST", path, opts.body);
        toast(opts.done ? opts.done(res) : label + ": done");
        refresh();
      } catch (e) {
        toast(e.message, true);
      } finally {
        b.disabled = false;
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

const enc = encodeURIComponent;

function renderService(o) {
  const s = o.service;
  const chips = [
    el("span.chip", {}, "v" + s.version.replace(/^v/, "")),
    el("span.chip", {}, s.incus ? "Incus connected" : "Incus not connected"),
    el("span.chip", {}, s.cached_secrets + " secrets cached ",
      action("forget", "/api/secrets/forget", { cls: "link", done: (r) => "Forgot " + r.count + " secrets" })),
    el("span.chip", {}, o.idle_stop ? "idle stop after " + o.idle_stop : "idle stop off"),
    el("span.chip", { title: "the key containers sign commits with" },
      o.signing ? "signing " + o.signing.slice(0, 18) + "…" : "commit signing off"),
  ];
  if (o.proxy.http_port && o.proxy.http_port !== 80) {
    chips.push(el("span.chip.warn", { title: "run silo setup so silo may use ports 80 and 443" },
      "proxy on ports " + o.proxy.http_port + "/" + o.proxy.https_port));
  }
  $("#service").replaceChildren(...chips);
}

function urlLinks(c) {
  if (!c.urls.length) return null;
  return el("div.row.urls", {}, c.urls.map((u) => {
    const href = u.https_url || u.url;
    return el("a", { href, target: "_blank", rel: "noopener" }, href.replace(/\/$/, ""));
  }));
}

function daemonTable(c) {
  if (!c.daemons.length) return null;
  const running = c.state === "running";
  const base = "/api/containers/" + enc(c.name) + "/daemons/";
  return el("table", {}, el("tbody", {}, c.daemons.map((d) => el("tr", {},
    el("td", {}, el("span.dot." + d.state, { title: d.state }), " ", el("strong", {}, d.name),
      d.autostart ? el("span.muted", {}, " autostart") : null),
    el("td.cmd.mono", { title: d.cmd }, d.cmd),
    el("td.actions", {},
      running && d.state !== "running" ? action("start", base + enc(d.name) + "/start") : null,
      running && d.state === "running" ? action("restart", base + enc(d.name) + "/restart") : null,
      running && d.state === "running" ? action("stop", base + enc(d.name) + "/stop") : null,
      el("button", { type: "button", onclick: () => showLogs(c.name, d.name) }, "logs")),
  ))));
}

function snapshotsBlock(c) {
  const list = el("div.snapshots");
  const toggle = el("button.link", {
    type: "button",
    onclick: async () => {
      if (list.classList.toggle("open")) await loadSnapshots(c, list);
    },
  }, c.snapshots + " snapshots");
  return [el("div.row", {}, toggle,
    action("take snapshot", "/api/containers/" + enc(c.name) + "/snapshots", { cls: "link", done: (r) => "Took " + r.name })), list];
}

async function loadSnapshots(c, list) {
  list.replaceChildren(el("p.muted", {}, "Loading…"));
  try {
    const snaps = await api("GET", "/api/containers/" + enc(c.name) + "/snapshots");
    if (!snaps.length) return list.replaceChildren(el("p.muted", {}, "No snapshots."));
    list.replaceChildren(el("table", {}, el("tbody", {}, snaps.slice().reverse().map((s) => el("tr", {},
      el("td.mono", {}, s.Name),
      el("td.muted", {}, s.CreatedAt),
      el("td.actions", {}, action("restore", "/api/containers/" + enc(c.name) + "/snapshots/" + enc(s.Name) + "/restore", {
        cls: "danger",
        confirm: "Restore " + c.label + " to " + s.Name + "? Changes in the container since then are lost (the mounted project directory isn't affected).",
      })),
    )))));
  } catch (e) {
    list.replaceChildren(el("p.warn", {}, e.message));
  }
}

function renderContainer(c) {
  const path = "/api/containers/" + enc(c.name) + "/";
  const running = c.state === "running";
  const meta = [
    c.project_dir ? el("code", { title: "project directory" }, c.project_dir) : null,
    c.project_dir ? el("button.link", {
      type: "button",
      title: "copy a command that runs the default agent",
      onclick: () => navigator.clipboard.writeText("cd " + JSON.stringify(c.project_dir) + " && silo ra")
        .then(() => toast("Copied")),
    }, "copy cd && silo ra") : null,
    running && c.memory_bytes ? el("span.muted", {}, bytes(c.memory_bytes)) : null,
    running && c.ipv4 ? el("span.muted.mono", {}, c.ipv4) : null,
    running && c.last_active ? el("span.muted", {}, "used " + ago(c.last_active)) : null,
  ];
  return el("article.card." + (running ? "running" : "stopped"), {},
    el("div.row.spread", {},
      el("div.row", {}, el("h3", {}, c.label), el("span.pill." + c.state, {}, c.state), c.tmp ? el("span.chip", {}, "tmp") : null),
      el("div.row", {},
        !running ? action("start", path + "start", { cls: "primary" }) : null,
        running ? action("restart", path + "restart") : null,
        running ? action("stop", path + "stop", { cls: "danger" }) : null)),
    el("div.row", {}, meta),
    c.config_error ? el("p.warn", {}, c.config_error) : null,
    urlLinks(c),
    daemonTable(c),
    c.ports.length ? el("div.chips", {}, c.ports.map((p) =>
      el("span.chip.mono", { title: p.name || "" }, p.container + " → localhost:" + p.host + (p.name ? " " + p.name : "")))) : null,
    ...snapshotsBlock(c),
  );
}

function renderBases(o) {
  if (!o.bases.length) return $("#bases").replaceChildren(el("p.muted", {}, "No base templates yet."));
  $("#bases").replaceChildren(el("table", {}, el("tbody", {}, o.bases.map((b) => el("tr", {},
    el("td", {}, el("strong.mono", {}, b.image), el("div.muted", {}, b.refreshing ? "rebuilding…" : "built " + ago(b.built_at))),
    el("td.actions", {}, action("rebuild", "/api/bases/refresh", { body: { image: b.image }, done: (r) => r.started ? "Rebuilding" : "Already rebuilding" })),
  )))));
}

let current;
async function refresh() {
  try {
    const o = await api("GET", "/api/overview");
    current = o;
    renderService(o);
    renderBases(o);
    $("#containers").replaceChildren(...(o.containers.length
      ? o.containers.map(renderContainer)
      : [el("p.muted", {}, o.service.incus ? "No silo containers. Run silo up in a project." : "The silo service can't reach Incus.")]));
  } catch (e) {
    toast("Can't load: " + e.message, true);
  }
}

// Logs dialog.
let logTarget;
async function showLogs(container, daemon) {
  logTarget = [container, daemon];
  $("#dialog-title").textContent = daemon + " in " + container.replace(/^silo-/, "");
  $("#dialog-body").replaceChildren(el("p.muted", {}, "Loading…"));
  if (!$("#dialog").open) $("#dialog").showModal();
  try {
    const r = await api("GET", "/api/containers/" + enc(container) + "/daemons/" + enc(daemon) + "/logs");
    const pre = el("pre", {}, r.text || "(no output)");
    $("#dialog-body").replaceChildren(pre);
    $("#dialog").scrollTop = $("#dialog").scrollHeight;
  } catch (e) {
    $("#dialog-body").replaceChildren(el("p.warn", {}, e.message));
  }
}
$("#dialog-close").addEventListener("click", () => $("#dialog").close());
$("#dialog-refresh").addEventListener("click", () => logTarget && showLogs(...logTarget));

// Events: shown in the list, and they trigger a refresh.
let refreshTimer;
function onEvent(e) {
  const list = $("#events");
  if (list.firstElementChild && list.firstElementChild.classList.contains("muted")) list.replaceChildren();
  const t = new Date(e.time);
  list.prepend(el("li", {},
    el("time", { datetime: e.time }, t.toLocaleTimeString()),
    e.type.replaceAll("-", " "),
    e.container ? el("strong", {}, " " + e.container.replace(/^silo-/, "")) : null,
    e.message ? el("div.muted", {}, e.message) : null));
  while (list.children.length > 60) list.lastElementChild.remove();
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(refresh, 400);
}
const events = new EventSource("/api/events");
events.onmessage = (m) => { try { onEvent(JSON.parse(m.data)); } catch (_) {} };

refresh();
setInterval(() => { if (!document.hidden && !$("#dialog").open) refresh(); }, 5000);
document.addEventListener("visibilitychange", () => { if (!document.hidden) refresh(); });
