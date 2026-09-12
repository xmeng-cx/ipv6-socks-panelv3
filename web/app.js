const $ = (id) => document.getElementById(id);
let system = null;
let toastTimer = null;
let accountUsers = [];

async function api(path, options = {}) {
  const response = await fetch(path, {headers:{"Content-Type":"application/json"}, ...options});
  if (response.status === 401) { location.href = "/login.html"; throw new Error("登录已过期"); }
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
  if (proxy.protocol === "hy2") {
    const endpointHost = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host;
    const obfsPassword = system.hy2ObfsPassword;
    const finalMask = {udp:[{type:"salamander",settings:{password:obfsPassword}}]};
    const params = new URLSearchParams({security:"tls",fp:"chrome",alpn:"h3,h2,http/1.1",sni:host,insecure:"1",obfs:"salamander","obfs-password":obfsPassword,fm:JSON.stringify(finalMask)});
    return `hysteria2://${encodeURIComponent(proxy.password)}@${endpointHost}:${proxy.port}?${params.toString()}#HY2-${proxy.port}`;
  }
  return `socks5://${encodeURIComponent(proxy.username)}:${encodeURIComponent(proxy.password)}@${host}:${proxy.port}`;
}

async function copyProxy(proxy) {
  await copyText(connectionURI(proxy));
  toast(`已复制端口 ${proxy.port} 的连接地址`);
}

async function copyText(value) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(value);
      return;
    } catch (_) {}
  }
  const input = document.createElement("textarea");
  input.value = value;
  input.setAttribute("readonly", "");
  input.style.position = "fixed";
  input.style.opacity = "0";
  document.body.appendChild(input);
  input.focus();
  input.select();
  input.setSelectionRange(0, value.length);
  const copied = document.execCommand("copy");
  input.remove();
  if (!copied) throw new Error("浏览器拒绝访问剪贴板，请手动复制");
}

function timeText(value) {
  if (!value || value.startsWith("0001-")) return "从未";
  return new Intl.DateTimeFormat("zh-CN", {month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit",second:"2-digit"}).format(new Date(value));
}

function renderRows(proxies) {
  const rows = $("proxyRows");
  if (!proxies.length) { rows.innerHTML = '<tr><td colspan="6" class="empty">还没有线路，点击“新增线路”开始。</td></tr>'; return; }
  rows.innerHTML = proxies.map(p => `<tr>
    <td><span class="port">${p.port}</span></td>
    <td><span class="muted">${p.protocol === "hy2" ? "Hysteria2" : "SOCKS5"}</span></td>
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
    $("networkPrefixMetric").classList.toggle("hidden", !health.isAdmin);
    $("networkInterfaceMetric").classList.toggle("hidden", !health.isAdmin);
    if (health.isAdmin) {
      $("prefix").textContent = health.network.prefix;
      $("iface").textContent = health.network.interface;
    }
    $("proxyCount").textContent = `${health.proxyCount} / ${health.maxProxies}`;
    $("username").textContent = health.username;
    $("currentUser").textContent = `${health.username}${health.isAdmin ? " · 管理员" : ""}`;
    $("subscriptionUrl").value = health.subscriptionUrl || "";
    $("editPrefixBtn").classList.toggle("hidden", !health.isAdmin);
    $("addBtn").classList.toggle("hidden", !health.isAdmin);
    $("userAdmin").classList.toggle("hidden", !health.isAdmin);
    $("rangeHint").textContent = `端口 ${health.basePort}–${health.basePort + health.maxProxies - 1} · TCP${health.udp ? " + UDP" : ""}`;
    const badge = $("healthBadge"); badge.className = "health ok"; badge.innerHTML = "<span></span>系统正常";
    renderRows(list.proxies);
    if (health.isAdmin) await refreshUsers();
  } catch (error) {
    const badge = $("healthBadge"); badge.className = "health error"; badge.innerHTML = "<span></span>系统异常";
    toast(error.message, true);
  }
}

async function createProxy(event) {
  event.preventDefault();
  if (event.submitter?.value === "cancel") { $("addDialog").close(); return; }
  const manual = $("manualPort").checked;
  const protocol = $("protocolInput").value;
  const owner = $("proxyOwnerInput").value;
  const count = Number($("proxyCountInput").value);
  const port = Number($("portInput").value);
  if (!owner) { toast("请选择线路账号", true); return; }
  if (!Number.isInteger(count) || count < 1 || count > system.maxProxies) { toast(`创建数量必须为 1–${system.maxProxies}`, true); return; }
  if (manual && count !== 1) { toast("手动指定端口时只能创建 1 条线路", true); return; }
  if (manual && (!Number.isInteger(port) || port < 1 || port > 65535)) { toast("请输入有效端口", true); return; }
  $("confirmAdd").disabled = true;
  try {
    const payload = manual ? {port, protocol, owner, count} : {protocol, owner, count};
    const result = await api("/api/v1/proxies", {method:"POST", body:JSON.stringify(payload)});
    $("addDialog").close(); toast(`已为 ${owner} 创建 ${result.count} 条线路`); await refresh();
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

async function refreshUsers() {
  const result = await api("/api/v1/users");
  accountUsers = result.users;
  $("userRows").innerHTML = result.users.map(user => `<tr><td><strong>${escapeText(user.username)}</strong></td><td>${user.role === "admin" ? "管理员" : "普通用户"}</td><td>${user.proxyCount}</td><td><button class="smallBtn" data-copy-sub="${escapeText(user.username)}">复制链接</button></td><td class="right">${user.role === "admin" ? "—" : `<button class="smallBtn danger" data-delete-user="${escapeText(user.username)}">删除</button>`}</td></tr>`).join("");
  $("userRows").querySelectorAll("[data-copy-sub]").forEach(button => button.addEventListener("click", async () => {
    const user = result.users.find(item => item.username === button.dataset.copySub);
    try { await copyText(user.subscriptionUrl); toast(`已复制 ${user.username} 的订阅链接`); }
    catch (error) { toast(error.message, true); }
  }));
  $("userRows").querySelectorAll("[data-delete-user]").forEach(button => button.addEventListener("click", () => deleteUser(button.dataset.deleteUser, button)));
}

async function createUser(event) {
  event.preventDefault();
  if (event.submitter?.value === "cancel") { $("userDialog").close(); return; }
  const username = $("newUsername").value.trim(), password = $("newPassword").value;
  $("confirmUser").disabled = true;
  try {
    await api("/api/v1/users", {method:"POST", body:JSON.stringify({username,password})});
    $("userDialog").close(); toast(`用户 ${username} 已创建，并建立了 10 条 HY2 线路`); await refreshUsers();
  } catch (error) { toast(error.message, true); }
  finally { $("confirmUser").disabled = false; }
}

async function deleteUser(username, button) {
  if (!confirm(`确认删除用户 ${username}？该用户的全部线路和 IPv6 将同时释放。`)) return;
  button.disabled = true;
  try { await api(`/api/v1/users/${encodeURIComponent(username)}`, {method:"DELETE"}); toast(`用户 ${username} 已删除`); await refreshUsers(); }
  catch (error) { toast(error.message, true); button.disabled = false; }
}

async function logout() { await api("/api/v1/auth/logout", {method:"POST", body:"{}"}); location.href = "/login.html"; }

async function updatePrefix(event) {
  event.preventDefault();
  if (event.submitter?.value === "cancel") { $("prefixDialog").close(); return; }
  const prefix = $("prefixInput").value.trim();
  const interfaceName = $("interfaceInput").value.trim();
  const message = prefix || interfaceName ? `确认将全部线路切换到 ${interfaceName || "自动网卡"} / ${prefix || "自动前缀"}？` : "确认恢复自动识别 IPv6 网络？";
  if (!confirm(message)) return;
  $("confirmPrefix").disabled = true;
  try {
    toast("正在验证新前缀并迁移线路…");
    const result = await api("/api/v1/network", {method:"PUT", body:JSON.stringify({interface:interfaceName, prefix})});
    $("prefixDialog").close();
    toast(`IPv6 前缀已更新为 ${result.network.prefix}`);
    await refresh();
  } catch (error) { toast(error.message, true); }
  finally { $("confirmPrefix").disabled = false; }
}

async function updateDirectRules(event) {
  event.preventDefault();
  if (event.submitter?.value === "cancel") { $("directDialog").close(); return; }
  const directRules = $("directRulesInput").value.split(/\r?\n/).map(value => value.trim()).filter(Boolean);
  $("confirmDirect").disabled = true;
  try {
    const result = await api("/api/v1/subscription/settings", {method:"PUT", body:JSON.stringify({directRules})});
    system.directRules = result.directRules;
    $("directDialog").close();
    toast(`直连设置已保存，共 ${result.directRules.length} 项`);
  } catch (error) { toast(error.message, true); }
  finally { $("confirmDirect").disabled = false; }
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

$("addBtn").addEventListener("click", () => {
  $("manualPort").checked = false;
  $("manualPort").disabled = false;
  $("portField").classList.add("hidden");
  $("portInput").value = "";
  $("protocolInput").value = "hy2";
  $("proxyCountInput").value = "1";
  $("proxyCountInput").max = system?.maxProxies || 100;
  $("proxyOwnerInput").innerHTML = accountUsers.map(user => `<option value="${escapeText(user.username)}">${escapeText(user.username)}${user.role === "admin" ? "（管理员）" : ""}</option>`).join("");
  $("addDialog").showModal();
});
$("manualPort").addEventListener("change", (event) => $("portField").classList.toggle("hidden", !event.target.checked));
$("proxyCountInput").addEventListener("input", (event) => {
  const batch = Number(event.target.value) > 1;
  $("manualPort").disabled = batch;
  if (batch) { $("manualPort").checked = false; $("portField").classList.add("hidden"); }
});
$("addForm").addEventListener("submit", createProxy);
$("editPrefixBtn").addEventListener("click", () => { $("interfaceInput").value = system?.network?.interface || ""; $("prefixInput").value = system?.network?.prefix || ""; $("prefixDialog").showModal(); });
$("prefixForm").addEventListener("submit", updatePrefix);
$("rotateAllBtn").addEventListener("click", rotateAll);
$("addUserBtn").addEventListener("click", () => { $("newUsername").value=""; $("newPassword").value=""; $("userDialog").showModal(); });
$("userForm").addEventListener("submit", createUser);
$("directSettingsBtn").addEventListener("click", () => { $("directRulesInput").value = (system?.directRules || []).join("\n"); $("directDialog").showModal(); });
$("directForm").addEventListener("submit", updateDirectRules);
$("logoutBtn").addEventListener("click", logout);
$("copySubscriptionBtn").addEventListener("click", async () => {
  try { await copyText($("subscriptionUrl").value); toast("Mihomo 订阅链接已复制"); }
  catch (error) { toast(error.message, true); }
});
refresh();
setInterval(refresh, 10000);
