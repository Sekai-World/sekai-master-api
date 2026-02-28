import { requestJSON } from "./api.js";
import { clearToken, token } from "./auth.js";

const escapeHTML = (value) =>
  String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/\"/g, "&quot;")
    .replace(/'/g, "&#39;");

const profileItem = (label, value) => `
  <div class="profile-item">
    <span class="profile-label">${escapeHTML(label)}</span>
    <span class="profile-value">${escapeHTML(value || "-")}</span>
  </div>
`;

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
  healthView.textContent = JSON.stringify(health.payload, null, 2);

  const profile = await requestJSON("/api/v1/admin/profile", { bearer });
  if (!profile.ok) {
    clearToken();
    window.location.href = "/admin/login";
    return;
  }

  const user = profile.payload?.user ?? {};
  profileView.innerHTML =
    profileItem("用户名", user.username) +
    profileItem("显示名", user.display_name) +
    profileItem("邮箱", user.email) +
    profileItem("用户ID", user.id);
};
