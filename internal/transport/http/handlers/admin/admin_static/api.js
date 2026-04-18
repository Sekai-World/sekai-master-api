export const requestJSON = async (url, options = {}) => {
  const headers = {};
  if (options.bearer) {
    headers.Authorization = `Bearer ${options.bearer}`;
  }
  if (options.json) {
    headers["Content-Type"] = "application/json";
  }

  const response = await fetch(url, {
    method: options.method ?? "GET",
    headers,
    body: options.json ? JSON.stringify(options.json) : undefined,
  });

  const payload = await response.json().catch(() => ({ message: "invalid json response" }));

  return {
    ok: response.ok,
    status: response.status,
    payload,
  };
};
