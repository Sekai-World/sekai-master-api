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
  if (normalized === "down" || normalized === "error") {
    return "status-down";
  }
  return "";
};

const formatStatusDisplay = (status) => String(status ?? "-").toUpperCase();

export const initDashboardPage = async () => {
  const healthView = document.getElementById("health-view");
  const profileView = document.getElementById("profile-view");

  if (!healthView || !profileView) {
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
};
