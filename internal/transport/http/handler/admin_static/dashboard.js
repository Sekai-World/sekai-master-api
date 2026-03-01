import { requestJSON } from "./api.js";
import { clearToken, token } from "./auth.js";

const escapeHTML = (value) =>
  String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/\"/g, "&quot;")
    .replace(/'/g, "&#39;");

const infoItem = (label, value, valueClass = "") => `
  <div class="profile-item">
    <span class="profile-label">${escapeHTML(label)}</span>
    <span class="profile-value ${escapeHTML(valueClass)}">${escapeHTML(value || "-")}</span>
  </div>
`;

const statusValueClass = (status) => {
  const normalized = String(status ?? "").toLowerCase();
  if (normalized === "ok" || normalized === "up") {
    return "status-up";
  }
  if (normalized === "success") {
    return "status-up";
  }
  if (normalized === "pending") {
    return "status-pending";
  }
  if (normalized === "down" || normalized === "error") {
    return "status-down";
  }
  if (normalized === "failed") {
    return "status-down";
  }
  return "";
};

const formatStatusDisplay = (status) => String(status ?? "-").toUpperCase();

const formatTime = (value) => {
  if (!value) {
    return "-";
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }

  return date.toLocaleString();
};

const formatDuration = (value) => {
  if (value === null || value === undefined) {
    return "-";
  }

  const ms = Number(value);
  if (Number.isNaN(ms) || ms < 0) {
    return "-";
  }

  if (ms < 1000) {
    return `${ms} ms`;
  }

  return `${(ms / 1000).toFixed(2)} s`;
};

const firstErrorMessage = (payload) =>
  payload?.error?.message || payload?.message || payload?.error || "请求失败";

const renderMasterDataItems = (items) => {
  if (!Array.isArray(items) || items.length === 0) {
    return '<div class="profile-item"><span class="profile-label">状态</span><span class="profile-value">暂无同步记录</span></div>';
  }

  return items
    .map(
      (item) => `
      <div class="master-data-status-item">
        <div><span class="label">地区</span><span class="value">${escapeHTML(item.region || "-")}</span></div>
        <div><span class="label">状态</span><span class="value ${escapeHTML(statusValueClass(item.status))}">${escapeHTML(formatStatusDisplay(item.status))}</span></div>
        <div><span class="label">文件数</span><span class="value">${escapeHTML(String(item.file_count ?? "-"))}</span></div>
        <div><span class="label">耗时</span><span class="value">${escapeHTML(formatDuration(item.sync_duration_ms))}</span></div>
        <div><span class="label">上次同步</span><span class="value">${escapeHTML(formatTime(item.last_synced_at))}</span></div>
        <div><span class="label">错误</span><span class="value">${escapeHTML(item.error_message || "-")}</span></div>
      </div>
    `,
    )
    .join("");
};

export const initDashboardPage = async () => {
  const healthView = document.getElementById("health-view");
  const profileView = document.getElementById("profile-view");
  const masterDataStatusView = document.getElementById("master-data-status-view");
  const syncButton = document.getElementById("sync-master-data-button");
  const syncMessage = document.getElementById("sync-message");

  if (!healthView || !profileView || !masterDataStatusView || !syncButton || !syncMessage) {
    return;
  }

  const bearer = token();
  if (!bearer) {
    window.location.href = "/admin/login";
    return;
  }

  const logoutButton = document.getElementById("logout");
  logoutButton?.addEventListener("click", () => {
    clearToken();
    window.location.href = "/admin/login";
  });

  const health = await requestJSON("/api/v1/health");
  const healthPayload = health.ok ? health.payload ?? {} : {};
  const serviceStatus = healthPayload.status ?? "unknown";
  const databaseStatus = healthPayload.database ?? "unknown";

  healthView.innerHTML =
    infoItem("服务", formatStatusDisplay(serviceStatus), statusValueClass(serviceStatus)) +
    infoItem("数据库", formatStatusDisplay(databaseStatus), statusValueClass(databaseStatus));

  const profile = await requestJSON("/api/v1/admin/profile", { bearer });
  if (!profile.ok) {
    clearToken();
    window.location.href = "/admin/login";
    return;
  }

  const user = profile.payload?.user ?? {};
  profileView.innerHTML =
    infoItem("用户名", user.username) +
    infoItem("显示名", user.display_name) +
    infoItem("邮箱", user.email) +
    infoItem("用户ID", user.id);

  const loadMasterDataStatus = async () => {
    const statusResult = await requestJSON("/api/v1/master-data/status");
    if (!statusResult.ok) {
      masterDataStatusView.innerHTML = '<div class="profile-item"><span class="profile-label">错误</span><span class="profile-value">加载同步状态失败</span></div>';
      return;
    }

    masterDataStatusView.innerHTML = renderMasterDataItems(statusResult.payload?.items ?? []);
  };

  await loadMasterDataStatus();

  const eventSource = new EventSource("/api/v1/master-data/events");
  eventSource.addEventListener("master_data_updated", async (event) => {
    let payload = null;
    try {
      payload = JSON.parse(event.data);
    } catch {
      payload = null;
    }

    syncMessage.classList.remove("is-error");
    syncMessage.classList.add("is-success");
    syncMessage.textContent = payload?.status === "failed" ? "检测到同步更新（含失败项），已刷新状态" : "检测到同步更新，已刷新状态";
    await loadMasterDataStatus();
  });

  window.addEventListener("beforeunload", () => {
    eventSource.close();
  });

  syncButton.addEventListener("click", async () => {
    syncButton.disabled = true;
    syncButton.classList.add("is-loading");
    syncMessage.classList.remove("is-error", "is-success");
    syncMessage.textContent = "正在同步 Master Data...";

    const syncResult = await requestJSON("/api/v1/admin/master-data/sync", {
      method: "POST",
      bearer,
    });

    if (syncResult.status === 401) {
      clearToken();
      window.location.href = "/admin/login";
      return;
    }

    if (!syncResult.ok) {
      syncMessage.classList.add("is-error");
      syncMessage.textContent = firstErrorMessage(syncResult.payload);
      await loadMasterDataStatus();
      syncButton.disabled = false;
      syncButton.classList.remove("is-loading");
      return;
    }

    syncMessage.classList.add("is-success");
    syncMessage.textContent = "同步完成";
    await loadMasterDataStatus();
    syncButton.disabled = false;
    syncButton.classList.remove("is-loading");
  });
};
