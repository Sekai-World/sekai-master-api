(function () {
  var tokenKey = "sekai_admin_token";

  function token() {
    return sessionStorage.getItem(tokenKey) || "";
  }

  function saveToken(value) {
    sessionStorage.setItem(tokenKey, value);
  }

  function clearToken() {
    sessionStorage.removeItem(tokenKey);
  }

  function readErrorCode(payload) {
    if (!payload || !payload.error) {
      return "";
    }

    if (typeof payload.error.code === "string") {
      return payload.error.code;
    }

    return "";
  }

  function loginErrorMessage(result) {
    var code = readErrorCode(result.payload);

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
  }

  async function requestJSON(url, options) {
    options = options || {};

    var headers = {};
    if (options.bearer) {
      headers.Authorization = "Bearer " + options.bearer;
    }
    if (options.json) {
      headers["Content-Type"] = "application/json";
    }

    var response = await fetch(url, {
      method: options.method || "GET",
      headers: headers,
      body: options.json ? JSON.stringify(options.json) : undefined,
    });
    var payload = await response.json().catch(function () {
      return { message: "invalid json response" };
    });

    return {
      ok: response.ok,
      status: response.status,
      payload: payload,
    };
  }

  async function initLoginPage() {
    var form = document.getElementById("login-form");
    if (!form) {
      return;
    }

    var error = document.getElementById("login-error");

    var submitButton = form.querySelector("button[type='submit']");
    var defaultButtonText = submitButton ? submitButton.textContent : "登录 Dashboard";

    function setSubmitting(isSubmitting) {
      if (!submitButton) {
        return;
      }

      submitButton.disabled = isSubmitting;
      submitButton.textContent = isSubmitting ? "登录中..." : defaultButtonText;
      submitButton.classList.toggle("is-loading", isSubmitting);
    }

    form.addEventListener("submit", async function (event) {
      event.preventDefault();
      error.textContent = "";

      var username = document.getElementById("username").value.trim();
      var password = document.getElementById("password").value.trim();

      if (!username || !password) {
        error.textContent = "请先输入用户名和密码";
        return;
      }

      setSubmitting(true);

      try {
        var loginResult = await requestJSON("/api/v1/admin/login", {
          method: "POST",
          json: {
            username: username,
            password: password,
          },
        });

        if (!loginResult.ok || !loginResult.payload.access_token) {
          error.textContent = loginErrorMessage(loginResult);
          return;
        }

        saveToken(loginResult.payload.access_token);
        window.location.href = "/admin";
      } catch (_err) {
        error.textContent = "网络异常，无法连接登录服务";
      } finally {
        setSubmitting(false);
      }
    });
  }

  async function initDashboardPage() {
    var healthView = document.getElementById("health-view");
    var profileView = document.getElementById("profile-view");

    if (!healthView || !profileView) {
      return;
    }

    var bearer = token();
    if (!bearer) {
      window.location.href = "/admin/login";
      return;
    }

    var logoutButton = document.getElementById("logout");
    if (logoutButton) {
      logoutButton.addEventListener("click", function () {
        clearToken();
        window.location.href = "/admin/login";
      });
    }

    var health = await requestJSON("/api/v1/health");
    healthView.textContent = JSON.stringify(health.payload, null, 2);

    var profile = await requestJSON("/api/v1/admin/profile", { bearer: bearer });
    if (!profile.ok) {
      clearToken();
      window.location.href = "/admin/login";
      return;
    }

    profileView.textContent = JSON.stringify(profile.payload, null, 2);
  }

  initLoginPage();
  initDashboardPage();
})();
