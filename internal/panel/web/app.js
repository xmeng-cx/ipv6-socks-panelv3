const $ = (id) => document.getElementById(id);
let system = null;
let toastTimer = null;

async function api(path, options = {}) {
  const response = await fetch(path, {headers:{"Content-Type":"application/json"}, ...options});
  if (response.status === 204) return null;
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data?.error?.message || `请求失败 (${response.status})`);
  return data;
}

function toast(message, error = false) {
  const node = $("toast");
  node.textContent = message;
  node.className = `toast${error ? " error" : ""}`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => node.classList.add("hidden"), 3600);
}

function escapeText(value) {
  const node = document.createElement("span");
  node.textContent = value ?? "";
  return node.innerHTML;
}

function connectionURI(proxy) {
  const host = system?.advertiseHost || location.hostname;
  return `socks5://${encodeURIComponent(system.username)}:${encodeURIComponent(system.password)}@${host}:${proxy.port}`;
}

async function copyProxy(proxy) {
  await navigator.clipboard.writeText(connectionURI(proxy));
  toast(`已复制端口 ${proxy.port} 的连接地址`);
}

function timeText(value) {
  if (!value || value.startsWith("0001-")) return "从未";
  return new Intl.DateTimeFormat("zh-CN", {month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit",second:"2-digit"}).format(new Date(value));
}

function renderRows(proxies) {
  const rows = $("proxyRows");
  if (!proxies.length) { rows.innerHTML = '<tr><td colspan="5" class="empty">还没有线路，点击“新增 SOCKS5”开始。</td></tr>'; return; }
  rows.innerHTML = proxies.map(p => `<tr>
    <td><span class="port">${p.port}</span></td>
    <td><span class="ipv6">${escapeText(p.ipv6)}</span>${p.lastError ? `<div class="muted">${escapeText(p.lastError)}</div>` : ""}</td>
    <td><span class="status ${escapeText(p.status)}">${p.status === "healthy" ? "正常" : escapeText(p.status)}</span></td>
    <td class="muted">${timeText(p.lastRotatedAt)}</td>
    <td><div class="rowActions">
      <button class="smallBtn" data-action="copy" data-id="${p.id}">复制</button>
      <button class="smallBtn" data-action="rotate" data-id="${p.id}">换 IP</button>
      <button class="smallBtn danger" data-action="delete" data-id="${p.id}">删除</button>
    </div></td></tr>`).join("");
  rows.querySelectorAll("button").forEach(button => button.addEventListener("click", () => rowAction(button.dataset.action, button.dataset.id, proxies, button)));
}

async function rowAction(action, id, proxies, button) {
  const proxy = proxies.find(item => item.id === id);
  if (!proxy) return;
  if (action === "copy") return copyProxy(proxy);
  if (action === "delete" && !confirm(`确认删除端口 ${proxy.port}？绑定的 IPv6 将立即释放。`)) return;
  button.disabled = true;
  try {
    if (action === "rotate") {
      toast(`正在更换端口 ${proxy.port} 的 IPv6…`);
      const updated = await api(`/api/v1/proxies/${id}/rotate`, {method:"POST", body:"{}"});
      toast(`更换成功：${updated.ipv6}`);
    } else if (action === "delete") {
      await api(`/api/v1/proxies/${id}`, {method:"DELETE"});
      toast(`已删除端口 ${proxy.port}`);
    }
    await refresh();
  } catch (error) { toast(error.message, true); }
  finally { button.disabled = false; }
}

async function refresh() {
  try {
    const [health, list] = await Promise.all([api("/api/v1/health"), api("/api/v1/proxies")]);
    system = health;
    $("prefix").textContent = health.network.prefix;
    $("iface").textContent = health.network.interface;
    $("proxyCount").textContent = `${health.proxyCount} / ${health.maxProxies}`;
    $("username").textContent = health.username;
    $("rangeHint").textContent = `端口 ${health.basePort}–${health.basePort + health.maxProxies - 1} · TCP${health.udp ? " + UDP" : ""}`;
    const badge = $("healthBadge"); badge.className = "health ok"; badge.innerHTML = "<span></span>系统正常";
    renderRows(list.proxies);
  } catch (error) {
    const badge = $("healthBadge"); badge.className = "health error"; badge.innerHTML = "<span></span>系统异常";
    toast(error.message, true);
  }
}

async function createProxy(event) {
  event.preventDefault();
  if (event.submitter?.value === "cancel") { $("addDialog").close(); return; }
  const manual = $("manualPort").checked;
  const port = Number($("portInput").value);
  if (manual && (!Number.isInteger(port) || port < 1 || port > 65535)) { toast("请输入有效端口", true); return; }
  $("confirmAdd").disabled = true;
  try {
    const proxy = await api("/api/v1/proxies", {method:"POST", body:JSON.stringify(manual ? {port} : {})});
    $("addDialog").close(); toast(`线路已创建：端口 ${proxy.port}`); await refresh();
  } catch (error) { toast(error.message, true); }
  finally { $("confirmAdd").disabled = false; }
}

async function rotateAll() {
  if (!confirm("确认更换全部线路的 IPv6？每条线路会独立验证并切换。")) return;
  $("rotateAllBtn").disabled = true;
  try {
    const job = await api("/api/v1/proxies/rotate-all", {method:"POST", body:"{}"});
    $("jobPanel").classList.remove("hidden");
    await pollJob(job.id);
  } catch (error) { toast(error.message, true); $("rotateAllBtn").disabled = false; }
}

async function pollJob(id) {
  try {
    const job = await api(`/api/v1/jobs/${id}`);
    $("jobText").textContent = `${job.completed} / ${job.total}`;
    $("jobProgress").style.width = `${job.total ? job.completed / job.total * 100 : 100}%`;
    if (job.status !== "completed") return setTimeout(() => pollJob(id), 900);
    toast(`全部更换完成：成功 ${job.succeeded}，失败 ${job.failed}`, job.failed > 0);
    $("rotateAllBtn").disabled = false;
    setTimeout(() => $("jobPanel").classList.add("hidden"), 2500);
    await refresh();
  } catch (error) { toast(error.message, true); $("rotateAllBtn").disabled = false; }
}

$("addBtn").addEventListener("click", () => { $("manualPort").checked = false; $("portField").classList.add("hidden"); $("portInput").value = ""; $("addDialog").showModal(); });
$("manualPort").addEventListener("change", (event) => $("portField").classList.toggle("hidden", !event.target.checked));
$("addForm").addEventListener("submit", createProxy);
$("rotateAllBtn").addEventListener("click", rotateAll);
refresh();
setInterval(refresh, 10000);
