const svg = document.querySelector("#graph");
const detail = document.querySelector("#detail");
const stats = document.querySelector("#stats");
const W = 1200;
const H = 760;

const colors = {
  fact: "#4f7c2b",
  rule: "#3f7ea8",
  project: "#b0762a",
  preference: "#b23fae",
  concept: "#6d9a34",
  decision: "#356123",
  profile: "#8ab63f",
};

let topology = { nodes: [], edges: [] };
let positions = [];
let hits = new Set();
let selected = "";
let view = { x: 0, y: 0, k: 1 };

document.querySelectorAll(".nav").forEach((button) => {
  button.addEventListener("click", () => {
    document.querySelectorAll(".nav").forEach((item) => item.classList.remove("active"));
    button.classList.add("active");
    const notes = button.dataset.view === "notes";
    document.querySelector("#graph-view").hidden = notes;
    document.querySelector("#notes-view").hidden = !notes;
    document.querySelector("h1").textContent = notes ? "笔记" : "知识图谱";
    if (notes) loadRecent();
  });
});

document.querySelector("#refresh").addEventListener("click", () => loadGraph());
document.querySelector("#trace").addEventListener("click", trace);
document.querySelector("#query").addEventListener("keydown", (event) => {
  if (event.key === "Enter") trace();
});
document.querySelector("#clear").addEventListener("click", () => {
  hits = new Set();
  document.querySelector("#clear").hidden = true;
  document.querySelector("#elapsed").textContent = "";
  render();
});
document.querySelector("#remember").addEventListener("submit", remember);

svg.addEventListener("wheel", (event) => {
  event.preventDefault();
  view.k = Math.min(3.2, Math.max(0.45, view.k * (event.deltaY < 0 ? 1.08 : 0.92)));
  render();
}, { passive: false });

let drag = null;
svg.addEventListener("pointerdown", (event) => {
  if (event.target.closest(".node")) return;
  drag = { x: event.clientX, y: event.clientY, ox: view.x, oy: view.y };
});
svg.addEventListener("pointermove", (event) => {
  if (!drag) return;
  view.x = drag.ox + (event.clientX - drag.x);
  view.y = drag.oy + (event.clientY - drag.y);
  render();
});
svg.addEventListener("pointerup", () => { drag = null; });

async function loadGraph() {
  const response = await fetch("/api/graph?isolated=1");
  topology = await response.json();
  positions = layout(topology.nodes, topology.edges);
  renderStats();
  render();
}

function renderStats() {
  const cards = [
    ["笔记", topology.total_notes],
    ["链接", topology.total_edges],
    ["孤立", topology.isolated_count],
    ["未解析", topology.unresolved],
    ["图上", topology.nodes.length],
  ];
  stats.innerHTML = cards.map(([label, value]) => `<div><span>${label}</span><strong>${value ?? 0}</strong></div>`).join("");
}

function layout(nodes, edges) {
  const points = nodes.map((_, i) => {
    const angle = (i / Math.max(1, nodes.length)) * Math.PI * 2;
    const wobble = 1 + 0.28 * Math.sin(i * 2.399);
    const radius = Math.min(W, H) * 0.34;
    return { x: W / 2 + Math.cos(angle) * radius * wobble, y: H / 2 + Math.sin(angle) * radius * wobble };
  });
  const index = new Map(nodes.map((node, i) => [node.id, i]));
  const links = edges
    .map((edge) => [index.get(edge.source), index.get(edge.target)])
    .filter((pair) => pair[0] !== undefined && pair[1] !== undefined);
  for (let tick = 0; tick < 180; tick += 1) step(points, links);
  return points;
}

function step(points, links) {
  for (let i = 0; i < points.length; i += 1) {
    for (let j = i + 1; j < points.length; j += 1) {
      let dx = points[i].x - points[j].x;
      let dy = points[i].y - points[j].y;
      const dist = Math.sqrt(dx * dx + dy * dy) || 1;
      const force = 4200 / (dist * dist);
      points[i].x += (dx / dist) * force;
      points[i].y += (dy / dist) * force;
      points[j].x -= (dx / dist) * force;
      points[j].y -= (dy / dist) * force;
    }
  }
  for (const [from, to] of links) {
    const dx = points[to].x - points[from].x;
    const dy = points[to].y - points[from].y;
    const dist = Math.sqrt(dx * dx + dy * dy) || 1;
    const force = (dist - 130) * 0.045;
    points[from].x += (dx / dist) * force;
    points[from].y += (dy / dist) * force;
    points[to].x -= (dx / dist) * force;
    points[to].y -= (dy / dist) * force;
  }
  for (const point of points) {
    point.x += (W / 2 - point.x) * 0.04;
    point.y += (H / 2 - point.y) * 0.04;
  }
}

function render() {
  const index = new Map(topology.nodes.map((node, i) => [node.id, i]));
  const edges = topology.edges.map((edge) => {
    const a = positions[index.get(edge.source)];
    const b = positions[index.get(edge.target)];
    if (!a || !b) return "";
    const hot = hits.size && hits.has(edge.source) && hits.has(edge.target);
    const dim = hits.size && !hot;
    return `<line class="edge${hot ? " hit" : ""}${dim ? " dim" : ""}" x1="${a.x}" y1="${a.y}" x2="${b.x}" y2="${b.y}"></line>`;
  }).join("");
  const nodes = topology.nodes.map((node, i) => {
    const point = positions[i];
    const dim = hits.size && !hits.has(node.id) ? " dim" : "";
    const selectedClass = node.id === selected ? " selected" : "";
    const unresolved = node.resolved ? "" : " unresolved";
    const radius = 8 + Math.min(node.degree, 8) * 1.7;
    const fill = node.resolved ? (colors[node.category] || "#6d9a34") : "none";
    const label = node.degree > 0 || node.id === selected || hits.has(node.id)
      ? `<text class="label" x="${point.x + radius + 4}" y="${point.y + 4}">${escapeHTML(node.title)}</text>`
      : "";
    return `<g class="node${dim}${selectedClass}${unresolved}" data-id="${escapeHTML(node.id)}">
      <circle cx="${point.x}" cy="${point.y}" r="${radius}" fill="${fill}"></circle>${label}</g>`;
  }).join("");
  svg.innerHTML = `<g transform="translate(${view.x} ${view.y}) scale(${view.k})">${edges}${nodes}</g>`;
  svg.querySelectorAll(".node").forEach((node) => {
    node.addEventListener("click", () => select(node.dataset.id));
  });
}

function select(id) {
  selected = id;
  const node = topology.nodes.find((item) => item.id === id);
  if (!node) return;
  const neighbours = topology.edges
    .filter((edge) => edge.source === id || edge.target === id)
    .map((edge) => edge.source === id ? edge.target : edge.source);
  const linked = neighbours
    .map((nid) => topology.nodes.find((item) => item.id === nid))
    .filter(Boolean)
    .map((item) => `<span class="chip">${escapeHTML(item.title)}</span>`)
    .join("");
  detail.innerHTML = `
    <p class="eyebrow">${escapeHTML(node.category || "link")} · ${escapeHTML(node.tier || "")}</p>
    <h2>${escapeHTML(node.title)}</h2>
    <p class="muted">${node.resolved ? escapeHTML(node.path || "") : "这个链接还没有对应笔记。"}</p>
    <p>${(node.tags || []).map((tag) => `<span class="chip">#${escapeHTML(tag)}</span>`).join("")}</p>
    <p class="muted">相连</p>
    <p>${linked || '<span class="muted">没有链接</span>'}</p>`;
  render();
}

async function trace() {
  const query = document.querySelector("#query").value.trim();
  if (!query) return;
  const depth = document.querySelector("#depth").value;
  const response = await fetch(`/api/recall?q=${encodeURIComponent(query)}&depth=${depth}`);
  const data = await response.json();
  hits = new Set();
  for (const result of data.results || []) {
    hits.add(result.entry.id);
    for (const hop of result.paths || []) {
      hits.add(hop.from_id);
      hits.add(hop.to_id);
    }
  }
  document.querySelector("#clear").hidden = false;
  document.querySelector("#elapsed").textContent = `${(data.results || []).length} 条 · ${data.elapsed}`;
  detail.innerHTML = `
    <p class="eyebrow">召回 · ${escapeHTML(data.elapsed || "")}</p>
    <h2>${escapeHTML(query)}</h2>
    <ol class="hit-list">${(data.results || []).map((result) => `
      <li><strong>${escapeHTML(result.entry.content)}</strong>
        <small>${escapeHTML(result.entry.category)} · ${escapeHTML(result.entry.tier)} · ${Number(result.score).toFixed(2)}${result.graph_score ? " · 图 " + Number(result.graph_score).toFixed(2) : ""}</small>
      </li>`).join("") || "<li>没有召回到笔记。</li>"}</ol>`;
  render();
}

async function remember(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const body = {
    content: form.content.value,
    category: form.category.value,
    tier: form.tier.value,
    links: form.links.value.split(/[,，]/).map((item) => item.trim()).filter(Boolean),
  };
  const response = await fetch("/api/remember", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    detail.textContent = await response.text();
    return;
  }
  form.reset();
  await loadGraph();
  await loadRecent();
}

async function loadRecent() {
  const response = await fetch("/api/recent");
  const notes = await response.json();
  document.querySelector("#recent").innerHTML = (notes || []).map((note) =>
    `<li>${escapeHTML(note.content)} <small>${escapeHTML(note.category)} · ${escapeHTML(note.tier)}</small></li>`
  ).join("");
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[char]));
}

loadGraph();
