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
  if (normalized === "running") {
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

const formatProgressMessage = (payload) => {
  const stepPart =
    Number(payload?.current_step) > 0 && Number(payload?.total_steps) > 0
      ? `[${payload.current_step}/${payload.total_steps}] `
      : "";

  const regionPart = payload?.region ? `${payload.region} ` : "";
  const phasePart = payload?.phase ? `${String(payload.phase).toUpperCase()} ` : "";
  const filePart = Number(payload?.file_count) > 0 ? `files=${payload.file_count} ` : "";
  const processedPart =
    Number(payload?.processed_files) > 0 && Number(payload?.total_files) > 0
      ? `progress=${payload.processed_files}/${payload.total_files} `
      : "";
  const failedPart = Number(payload?.failed_files) > 0 ? `failed=${payload.failed_files} ` : "";
  const filePathPart = payload?.file_path ? `file=${payload.file_path} ` : "";
  const durationPart = Number(payload?.duration_ms) >= 0 ? `${formatDuration(payload.duration_ms)} ` : "";
  const messagePart = payload?.message ? String(payload.message) : "";

  return `${stepPart}${regionPart}${phasePart}${processedPart}${filePart}${failedPart}${filePathPart}${durationPart}${messagePart}`.trim();
};

const firstErrorMessage = (payload) =>
  payload?.error?.message || payload?.message || payload?.error || "请求失败";

const toTimestamp = (value) => {
  if (!value) {
    return 0;
  }

  const time = new Date(value).getTime();
  if (Number.isNaN(time)) {
    return 0;
  }

  return time;
};

const renderMasterDataItems = (items) => {
  if (!Array.isArray(items) || items.length === 0) {
    return '<div class="profile-item"><span class="profile-label">状态</span><span class="profile-value">暂无同步记录</span></div>';
  }

  const sortedItems = [...items].sort((left, right) => {
    const leftTime = toTimestamp(left?.updated_at || left?.last_synced_at);
    const rightTime = toTimestamp(right?.updated_at || right?.last_synced_at);
    if (leftTime === rightTime) {
      return String(left?.region ?? "").localeCompare(String(right?.region ?? ""));
    }

    return rightTime - leftTime;
  });

  const latestByRegion = new Map();
  for (const item of sortedItems) {
    const regionKey = String(item?.region ?? "").trim().toLowerCase();
    if (!regionKey || latestByRegion.has(regionKey)) {
      continue;
    }
    latestByRegion.set(regionKey, item);
  }

  const regionItems = [...latestByRegion.values()];

  return regionItems
    .map(
      (item) => {
        const totalFiles = Number(item.total_files);
        const processedFiles = Number(item.processed_files);
        const failedFiles = Number(item.failed_files);
        const hasProgress = Number.isFinite(totalFiles) && totalFiles > 0;
        const safeProcessed = hasProgress ? Math.max(0, Math.min(totalFiles, Number.isFinite(processedFiles) ? processedFiles : 0)) : 0;
        const progressPercent = hasProgress ? Math.round((safeProcessed / totalFiles) * 100) : 0;
        const progressText = hasProgress
          ? `${safeProcessed}/${totalFiles}${failedFiles > 0 ? ` (失败 ${failedFiles})` : ""}`
          : "-";

        return `
      <div class="master-data-status-item">
        <div><span class="label">地区</span><span class="value">${escapeHTML(item.region || "-")}</span></div>
        <div><span class="label">状态</span><span class="value ${escapeHTML(statusValueClass(item.status))}">${escapeHTML(formatStatusDisplay(item.status))}</span></div>
        <div>
          <span class="label">进度</span>
          <span class="value">${escapeHTML(progressText)}</span>
          <div class="status-progress-track">
            <div class="status-progress-fill" style="width: ${progressPercent}%;"></div>
          </div>
        </div>
        <div><span class="label">文件数</span><span class="value">${escapeHTML(String(item.file_count ?? "-"))}</span></div>
        <div><span class="label">耗时</span><span class="value">${escapeHTML(formatDuration(item.sync_duration_ms))}</span></div>
        <div><span class="label">上次同步</span><span class="value">${escapeHTML(formatTime(item.last_synced_at))}</span></div>
        <div><span class="label">错误</span><span class="value">${escapeHTML(item.error_message || "-")}</span></div>
      </div>
    `;
      },
    )
    .join("");
};

export const initDashboardPage = async () => {
  const healthView = document.getElementById("health-view");
  const profileView = document.getElementById("profile-view");
  const masterDataStatusView = document.getElementById("master-data-status-view");
  const syncButton = document.getElementById("sync-master-data-button");
  const syncMessage = document.getElementById("sync-message");
  const syncProgressView = document.getElementById("sync-progress-view");

  if (!healthView || !profileView || !masterDataStatusView || !syncButton || !syncMessage || !syncProgressView) {
    return;
  }

  const progressHistory = [];
  const statusByRegion = new Map();
  const maxProgressHistory = 12;
  const renderProgressHistory = () => {
    if (progressHistory.length === 0) {
      syncProgressView.innerHTML = "暂无同步进度";
      return;
    }

    syncProgressView.innerHTML = progressHistory
      .map(
        (item) =>
          `<div class="sync-progress-item ${item.isError ? "is-error" : ""}">${escapeHTML(item.text)}</div>`,
      )
      .join("");
  };

  const pushProgressHistory = (text, isError = false) => {
    if (!text) {
      return;
    }

    progressHistory.unshift({ text, isError });
    if (progressHistory.length > maxProgressHistory) {
      progressHistory.length = maxProgressHistory;
    }
    renderProgressHistory();
  };

  const setStatusItems = (items) => {
    statusByRegion.clear();
    for (const item of Array.isArray(items) ? items : []) {
      const regionKey = String(item?.region ?? "").trim().toLowerCase();
      if (!regionKey) {
        continue;
      }

      const existing = statusByRegion.get(regionKey);
      const existingTime = toTimestamp(existing?.updated_at || existing?.last_synced_at);
      const incomingTime = toTimestamp(item?.updated_at || item?.last_synced_at);
      if (!existing || incomingTime >= existingTime) {
        statusByRegion.set(regionKey, item);
      }
    }
  };

  const upsertStatusItem = (item) => {
    const regionKey = String(item?.region ?? "").trim().toLowerCase();
    if (!regionKey) {
      return;
    }

    const existing = statusByRegion.get(regionKey);
    const existingTime = toTimestamp(existing?.updated_at || existing?.last_synced_at);
    const incomingTime = toTimestamp(item?.updated_at || item?.last_synced_at);
    if (!existing || incomingTime >= existingTime) {
      statusByRegion.set(regionKey, item);
    }
  };

  const upsertProgressItem = (payload) => {
    const regionKey = String(payload?.region ?? "").trim().toLowerCase();
    if (!regionKey) {
      return;
    }

    const existing = statusByRegion.get(regionKey) ?? { region: payload.region };
    const nowISO = new Date().toISOString();

    const merged = {
      ...existing,
      region: payload.region ?? existing.region,
      status:
        String(payload?.status ?? "").toLowerCase() === "running"
          ? "pending"
          : payload?.status ?? existing.status,
      processed_files:
        Number.isFinite(Number(payload?.processed_files)) && Number(payload?.processed_files) >= 0
          ? Number(payload.processed_files)
          : existing.processed_files,
      total_files:
        Number.isFinite(Number(payload?.total_files)) && Number(payload?.total_files) >= 0
          ? Number(payload.total_files)
          : existing.total_files,
      failed_files:
        Number.isFinite(Number(payload?.failed_files)) && Number(payload?.failed_files) >= 0
          ? Number(payload.failed_files)
          : existing.failed_files,
      updated_at: payload?.updated_at || existing.updated_at || nowISO,
      last_synced_at:
        String(payload?.status ?? "").toLowerCase() === "success" || String(payload?.status ?? "").toLowerCase() === "failed"
          ? payload?.updated_at || existing.last_synced_at || nowISO
          : existing.last_synced_at,
    };

    if (Number.isFinite(Number(payload?.file_count)) && Number(payload?.file_count) >= 0) {
      merged.file_count = Number(payload.file_count);
    }
    if (Number.isFinite(Number(payload?.duration_ms)) && Number(payload?.duration_ms) >= 0) {
      merged.sync_duration_ms = Number(payload.duration_ms);
    }

    statusByRegion.set(regionKey, merged);
  };

  const renderStatusFromMap = () => {
    masterDataStatusView.innerHTML = renderMasterDataItems([...statusByRegion.values()]);
  };

  renderProgressHistory();

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

    setStatusItems(statusResult.payload?.items ?? []);
    renderStatusFromMap();
  };

  let statusRefreshTimer = null;
  const scheduleStatusRefresh = () => {
    if (statusRefreshTimer) {
      clearTimeout(statusRefreshTimer);
    }

    statusRefreshTimer = setTimeout(async () => {
      statusRefreshTimer = null;
      await loadMasterDataStatus();
    }, 200);
  };

  await loadMasterDataStatus();

  const eventSource = new EventSource("/api/v1/master-data/events");
  eventSource.addEventListener("master_data_sync_progress", (event) => {
    let payload = null;
    try {
      payload = JSON.parse(event.data);
    } catch {
      payload = null;
    }

    const normalizedStatus = String(payload?.status ?? "").toLowerCase();
    syncMessage.classList.remove("is-error", "is-success");
    if (normalizedStatus === "failed") {
      syncMessage.classList.add("is-error");
    }

    const progressText = formatProgressMessage(payload);
    syncMessage.textContent = progressText || "同步进行中...";
    pushProgressHistory(syncMessage.textContent, normalizedStatus === "failed");

    if (payload?.region) {
      upsertProgressItem(payload);
      renderStatusFromMap();
    }

    if (normalizedStatus === "success" || normalizedStatus === "failed") {
      scheduleStatusRefresh();
    }
  });

  eventSource.addEventListener("master_data_status", (event) => {
    let payload = null;
    try {
      payload = JSON.parse(event.data);
    } catch {
      payload = null;
    }

    if (payload?.status_item) {
      upsertStatusItem(payload.status_item);
      renderStatusFromMap();
    }
  });

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
    pushProgressHistory(syncMessage.textContent, payload?.status === "failed");
    await loadMasterDataStatus();
  });

  window.addEventListener("beforeunload", () => {
    if (statusRefreshTimer) {
      clearTimeout(statusRefreshTimer);
      statusRefreshTimer = null;
    }
    eventSource.close();
  });

  syncButton.addEventListener("click", async () => {
    syncButton.disabled = true;
    syncButton.classList.add("is-loading");
    syncMessage.classList.remove("is-error", "is-success");
    syncMessage.textContent = "正在同步 Master Data...";
    pushProgressHistory(syncMessage.textContent, false);

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
      pushProgressHistory(syncMessage.textContent, true);
      await loadMasterDataStatus();
      syncButton.disabled = false;
      syncButton.classList.remove("is-loading");
      return;
    }

    syncMessage.classList.add("is-success");
    syncMessage.textContent = "同步完成";
    pushProgressHistory(syncMessage.textContent, false);
    await loadMasterDataStatus();
    syncButton.disabled = false;
    syncButton.classList.remove("is-loading");
  });
};
