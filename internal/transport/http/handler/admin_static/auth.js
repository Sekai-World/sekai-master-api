const tokenKey = "sekai_admin_token";
const loginStartPath = "/api/v1/admin/login";

export const token = () => sessionStorage.getItem(tokenKey) ?? "";
export const saveToken = (value) => sessionStorage.setItem(tokenKey, value);
export const clearToken = () => sessionStorage.removeItem(tokenKey);

const loginErrorMessage = (code) => {
  if (code === "auth_not_configured") {
    return "ZITADEL 登录尚未配置完成，请检查服务端环境变量";
  }

  if (code === "oauth_state_mismatch") {
    return "登录状态校验失败，请重新发起登录";
  }

  if (code === "oauth_exchange_failed") {
    return "认证服务换取令牌失败，请稍后重试";
  }

  if (code === "oauth_callback_failed") {
    return "认证回调处理失败，请检查服务端配置";
  }

  if (code === "oauth_login_failed") {
    return "认证服务拒绝了本次登录，请重新尝试";
  }

  return "";
};

export const initLoginPage = async () => {
  if (token()) {
    window.location.replace("/admin");
    return;
  }

  const loginButton = document.getElementById("login-button");
  const error = document.getElementById("login-error");
  if (!loginButton || !error) {
    return;
  }

  const params = new URLSearchParams(window.location.search);
  error.textContent = loginErrorMessage(params.get("error"));

  loginButton.addEventListener("click", async () => {
    loginButton.disabled = true;
    loginButton.classList.add("is-loading");
    error.textContent = "";
    window.location.href = loginStartPath;
  });
};
