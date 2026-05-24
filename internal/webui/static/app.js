const app = document.getElementById("app");
const updated = document.getElementById("updated");
let previous = null;
let previousAt = 0;

function bytes(value) {
  if (!Number.isFinite(value)) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let next = value;
  let unit = 0;
  while (next >= 1024 && unit < units.length - 1) {
    next /= 1024;
    unit += 1;
  }
  return next.toFixed(unit === 0 ? 0 : 1) + " " + units[unit];
}

function rate(current, path) {
  const now = Date.now();
  const currentValue = path.reduce((obj, key) => obj && obj[key], current) || 0;
  const previousValue = previous ? path.reduce((obj, key) => obj && obj[key], previous) || 0 : currentValue;
  const seconds = Math.max((now - previousAt) / 1000, 1);
  return Math.max(0, (currentValue - previousValue) / seconds);
}

function metric(label, value) {
  return "<div class=\"metric\"><div class=\"label\">" + label + "</div><div class=\"value\">" + value + "</div></div>";
}

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;", "'": "&#39;" }[c]));
}

function fmtTime(ts) {
  if (!ts) return "-";
  return new Date(ts).toLocaleString();
}

function render(data) {
  if (Array.isArray(data.rooms)) {
    renderServer(data);
  } else {
    renderClient(data);
  }
  updated.textContent = "Updated " + new Date().toLocaleTimeString();
}

function renderServer(data) {
  const peers = data.rooms.flatMap(room => (room.peers || []).map(peer => ({ room, peer })));
  peers.sort((a, b) => {
    const tA = new Date(a.peer.connected_at).getTime();
    const tB = new Date(b.peer.connected_at).getTime();
    if (tA !== tB) return tA - tB;
    return a.room.name < b.room.name ? -1 : a.room.name > b.room.name ? 1 : 0;
  });
  const rx = peers.reduce((sum, item) => sum + (item.peer.rx_bytes || 0), 0);
  const tx = peers.reduce((sum, item) => sum + (item.peer.tx_bytes || 0), 0);
  const roomRows = data.rooms.map(room => {
    const rRx = (room.peers || []).reduce((s, p) => s + (p.rx_bytes || 0), 0);
    const rTx = (room.peers || []).reduce((s, p) => s + (p.tx_bytes || 0), 0);
    return "<tr>" +
      "<td>" + esc(room.name) + "</td>" +
      "<td>" + (room.peers || []).length + "</td>" +
      "<td>" + fmtTime(room.created_at) + "</td>" +
      "<td>" + bytes(rRx) + "</td>" +
      "<td>" + bytes(rTx) + "</td>" +
    "</tr>";
  }).join("");
  const peerRows = peers.map(({ room, peer }) => "<tr>" +
    "<td>" + esc(peer.display_name || peer.id) + "</td>" +
    "<td>" + esc(room.name) + "</td>" +
    "<td>" + esc(peer.ip) + "</td>" +
    "<td>" + esc(peer.mac) + "</td>" +
    "<td>" + bytes(peer.rx_bytes || 0) + "</td>" +
    "<td>" + bytes(peer.tx_bytes || 0) + "</td>" +
    "<td>" + fmtTime(peer.connected_at) + "</td>" +
  "</tr>").join("");
  app.innerHTML =
    "<div class=\"grid\">" +
      metric("Rooms", data.rooms.length) +
      metric("Peers", peers.length) +
      metric("RX total", bytes(rx)) +
      metric("TX total", bytes(tx)) +
    "</div>" +
    "<section>" +
      "<h2>Rooms</h2>" +
      "<div class=\"table-wrap\"><table>" +
        "<thead><tr><th>Name</th><th>Peers</th><th>Created</th><th>RX</th><th>TX</th></tr></thead>" +
        "<tbody>" + (roomRows || "<tr><td colspan=\"5\" class=\"muted\">No active rooms</td></tr>") + "</tbody>" +
      "</table></div>" +
    "</section>" +
    "<section>" +
      "<h2>Peers</h2>" +
      "<div class=\"table-wrap\"><table>" +
        "<thead><tr><th>Name / ID</th><th>Room</th><th>IP</th><th>MAC</th><th>RX</th><th>TX</th><th>Connected</th></tr></thead>" +
        "<tbody>" + (peerRows || "<tr><td colspan=\"7\" class=\"muted\">No active peers</td></tr>") + "</tbody>" +
      "</table></div>" +
    "</section>";
}

function renderClient(data) {
  const peers = data.peers || [];
  const peerRows = peers.map(p =>
    "<tr>" +
      "<td>" + esc(p.display_name || p.id) + "</td>" +
      "<td>" + esc(p.ipv4 || "-") + "</td>" +
      "<td>" + esc(p.mac || "-") + "</td>" +
    "</tr>"
  ).join("");

  app.innerHTML =
    "<div class=\"grid\">" +
      metric("State", esc(data.state || "unknown")) +
      metric("Room", esc(data.room || "-")) +
      metric("Virtual IP", esc(data.ipv4 || "-")) +
      metric("Peers", peers.length) +
      metric("RX rate", bytes(rate(data, ["rx_bytes"])) + "/s") +
      metric("TX rate", bytes(rate(data, ["tx_bytes"])) + "/s") +
      metric("Reconnects", data.reconnects || 0) +
    "</div>" +
    "<section>" +
      "<h2>Session</h2>" +
      "<div class=\"table-wrap\"><table><tbody>" +
        "<tr><th>Server</th><td>" + esc(data.server || "-") + "</td></tr>" +
        "<tr><th>Peer ID</th><td>" + esc(data.peer_id || "-") + "</td></tr>" +
        "<tr><th>CIDR</th><td>" + esc(data.cidr || "-") + "</td></tr>" +
        "<tr><th>MAC</th><td>" + esc(data.mac || "-") + "</td></tr>" +
        "<tr><th>MTU</th><td>" + esc(data.mtu || "-") + "</td></tr>" +
        "<tr><th>Room created</th><td>" + fmtTime(data.room_created_at) + "</td></tr>" +
        "<tr><th>RX total</th><td>" + bytes(data.rx_bytes || 0) + "</td></tr>" +
        "<tr><th>TX total</th><td>" + bytes(data.tx_bytes || 0) + "</td></tr>" +
        "<tr><th>Last error</th><td>" + esc(data.last_error || "") + "</td></tr>" +
      "</tbody></table></div>" +
    "</section>" +
    "<section>" +
      "<h2>Peers</h2>" +
      "<div class=\"table-wrap\"><table>" +
        "<thead><tr><th>Name / ID</th><th>Virtual IP</th><th>MAC</th></tr></thead>" +
        "<tbody>" + (peerRows || "<tr><td colspan=\"3\" class=\"muted\">No peers</td></tr>") + "</tbody>" +
      "</table></div>" +
    "</section>";
}

async function refresh() {
  try {
    const res = await fetch("/api/status", { cache: "no-store" });
    const data = await res.json();
    render(data);
    previous = data;
    previousAt = Date.now();
  } catch (err) {
    app.innerHTML = "<pre>" + esc(err) + "</pre>";
  }
}

refresh();
setInterval(refresh, 1000);
