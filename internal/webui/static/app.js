// protolift control screen. Talks only to this computer's protolift.
"use strict";
const token = new URLSearchParams(location.search).get("t") || "";
const $ = (id) => document.getElementById(id);
const money = (v) => "$" + (v || 0).toFixed(2);
const cap = (v) => (v > 0 ? money(v) : "no cap");

async function call(path, body) {
  const res = await fetch(path, {
    method: body ? "POST" : "GET",
    headers: { "X-Protolift-Token": token, "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({ error: "protolift did not answer" }));
  if (!res.ok) throw new Error(data.error + (data.fix ? " — " + data.fix : ""));
  return data;
}

function el(tag, attrs, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else e.setAttribute(k, v);
  }
  for (const k of kids) if (k != null) e.append(k);
  return e;
}

function meter(label, spent, limit) {
  const pct = limit > 0 ? Math.min(100, (spent / limit) * 100) : 0;
  const bar = el("i", { class: pct >= 100 ? "over" : pct >= 80 ? "hot" : "" });
  bar.style.width = (limit > 0 ? pct : 0) + "%";
  return el("div", { class: "meter" }, el("span", {}, `${label} ${money(spent)} of ${cap(limit)}`), el("div", { class: "bar" }, bar));
}

function numberInput(value, min, step, label) {
  return el("input", { type: "number", min: String(min), step: String(step), value: String(value ?? 0), "aria-label": label });
}

function card(p) {
  const t = { dir: p.dir, envName: p.envName || "", cloud: p.cloud };
  const state = p.job && p.job.running ? "busy" : p.box ? p.box.state : "none";
  const label = { busy: "starting…", none: "no box" }[state] || state;
  const busy = state === "busy" || state === "pending" || state === "stopping";

  const act = async (btn, path, body) => {
    btn.disabled = true;
    try { await call(path, body); } catch (e) { alert(e.message); }
    btn.disabled = false;
    refresh();
  };
  const start = el("button", { class: "go", type: "button" }, p.box ? "Start" : "Create & start");
  start.disabled = busy || state === "running";
  start.addEventListener("click", () => act(start, "/api/start", t));
  const stop = el("button", { class: "stop", type: "button" }, "Stop");
  stop.disabled = state !== "running";
  stop.addEventListener("click", () => act(stop, "/api/stop", t));

  const daily = numberInput(p.budget.dailyUsd, 0, 0.5, "daily cap in dollars");
  const monthly = numberInput(p.budget.monthlyUsd, 0, 1, "monthly cap in dollars");
  const idle = numberInput(p.idleMinutes || 30, 5, 5, "idle minutes before the box stops");
  const maxh = numberInput(p.maxHours || 12, 1, 1, "maximum hours per run");
  const save = el("button", { type: "button" }, "Save");
  save.addEventListener("click", async () => {
    save.disabled = true;
    try {
      await call("/api/budget", { ...t, dailyUsd: +daily.value, monthlyUsd: +monthly.value });
      await call("/api/stops", { ...t, idleMinutes: +idle.value, maxHours: +maxh.value });
    } catch (e) { alert(e.message); }
    save.disabled = false;
    refresh();
  });

  const ports = (p.ports || []).map((n) => el("a", { href: `http://localhost:${n}`, target: "_blank", rel: "noopener noreferrer" }, `localhost:${n}`));
  return el("article", { class: "card" },
    el("div", { class: "head" },
      el("div", {}, el("div", { class: "name" }, p.name), el("div", { class: "where" }, `${p.cloud} · ${p.region}${p.box ? " · " + p.box.type : ""}${p.price ? " · " + p.price : ""}`)),
      el("span", { class: "pill " + state }, label)),
    el("div", { class: "money" }, meter("today", p.todayUsd, p.budget.dailyUsd), meter("month", p.monthUsd, p.budget.monthlyUsd)),
    ports.length ? el("div", { class: "ports" }, ...ports) : null,
    p.error ? el("div", { class: "err" }, p.error) : null,
    p.job && p.job.error ? el("div", { class: "err" }, p.job.error) : null,
    el("div", { class: "row" }, start, stop),
    el("details", {}, el("summary", {}, "Budget and stops"),
      el("div", { class: "row" },
        el("label", {}, "Daily $", daily), el("label", {}, "Monthly $", monthly)),
      el("div", { class: "row" },
        el("label", {}, "Stop after idle min", idle), el("label", {}, "Max hours", maxh), save)),
    p.job && p.job.log ? el("details", {}, el("summary", {}, "Last start log"), el("pre", {}, p.job.log)) : null,
    el("div", { class: "where" }, p.dir));
}

async function refresh() {
  try {
    const s = await call("/api/state");
    $("totals").textContent = `Today ${money(s.todayUsd)} · This month ${money(s.monthUsd)}`;
    if (document.activeElement?.closest("#global") == null) {
      $("g-daily").value = s.totalBudget.dailyUsd;
      $("g-monthly").value = s.totalBudget.monthlyUsd;
    }
    $("note").textContent = s.note;
    const list = $("projects");
    if (list.contains(document.activeElement)) return; // do not redraw while typing
    list.replaceChildren(...(s.projects.length
      ? s.projects.map(card)
      : [el("div", { class: "panel" }, "No projects yet. Run `protolift up` in a project folder; it appears here.")]));
  } catch (e) {
    $("totals").textContent = e.message;
  }
}

$("global").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  try {
    await call("/api/budget", { all: true, dailyUsd: +$("g-daily").value, monthlyUsd: +$("g-monthly").value });
  } catch (e) { alert(e.message); }
  refresh();
});

refresh();
setInterval(refresh, 10000);
