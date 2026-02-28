import { requestJSON } from "./api.js";

const tokenKey = "sekai_admin_token";

export const token = () => sessionStorage.getItem(tokenKey) ?? "";
export const saveToken = (value) => sessionStorage.setItem(tokenKey, value);
export const clearToken = () => sessionStorage.removeItem(tokenKey);

const readErrorCode = (payload) => payload?.error?.code ?? "";

const loginErrorMessage = (result) => {
  const code = readErrorCode(result.payload);

  if (result.status === 401 || code === "LOGIN_FAILED") {
    return "用户名或密码错误，请重试";
  }

  if (result.status === 502 || code === "KEYCLOAK_UNAVAILABLE") {
    return "认证服务暂不可用，请稍后重试";
  }

  if (result.status === 502 || code === "KEYCLOAK_RESPONSE_ERROR") {
    return "认证服务响应异常，请稍后重试";
  }

  if (result.status === 400 || code === "INVALID_REQUEST") {
    return "请求参数不正确，请检查输入后重试";
  }

  return "登录失败，请稍后重试";
};

export const initLoginPage = async () => {
  const form = document.getElementById("login-form");
  if (!form) {
    return;
  }

  const error = document.getElementById("login-error");
  const submitButton = form.querySelector("button[type='submit']");
  const defaultButtonText = submitButton?.textContent ?? "登录 Dashboard";

  const setSubmitting = (isSubmitting) => {
    if (!submitButton) {
      return;
    }

    submitButton.disabled = isSubmitting;
    submitButton.textContent = isSubmitting ? "登录中..." : defaultButtonText;
    submitButton.classList.toggle("is-loading", isSubmitting);
  };

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    error.textContent = "";

    const username = document.getElementById("username")?.value?.trim() ?? "";
    const password = document.getElementById("password")?.value?.trim() ?? "";

    if (!username || !password) {
      error.textContent = "请先输入用户名和密码";
      return;
    }

    setSubmitting(true);

    try {
      const loginResult = await requestJSON("/api/v1/admin/login", {
        method: "POST",
        json: { username, password },
      });

      if (!loginResult.ok || !loginResult.payload?.access_token) {
        error.textContent = loginErrorMessage(loginResult);
        return;
      }

      saveToken(loginResult.payload.access_token);
      window.location.href = "/admin";
    } catch {
      error.textContent = "网络异常，无法连接登录服务";
    } finally {
      setSubmitting(false);
    }
  });
};
