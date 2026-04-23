const BASE_URL = "/api";

interface RequestOptions extends RequestInit {
  params?: Record<string, string>;
}

class HttpError extends Error {
  constructor(
    public status: number,
    public statusText: string,
    public body: unknown,
  ) {
    super(`HTTP ${status}: ${statusText}`);
    this.name = "HttpError";
  }
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { params, ...fetchOptions } = options;

  let url = `${BASE_URL}${path}`;
  if (params) {
    const searchParams = new URLSearchParams(params);
    url += `?${searchParams.toString()}`;
  }

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(fetchOptions.headers as Record<string, string>),
  };

  const response = await fetch(url, { ...fetchOptions, headers });

  if (!response.ok) {
    const body = await response.json().catch(() => null);
    // Sensitive admin operations return 401 with { error, redirect } when the
    // user hasn't completed a recent step-up re-authentication. Bouncing the
    // browser to the redirect kicks off the Logto OIDC round-trip; the server
    // records auth_time on callback and the retried request passes through.
    if (response.status === 401 && isStepUpRequired(body)) {
      window.location.href = body.redirect;
    }
    throw new HttpError(response.status, response.statusText, body);
  }

  return response.json();
}

interface StepUpRequired {
  error: "step_up_required";
  redirect: string;
}

function isStepUpRequired(body: unknown): body is StepUpRequired {
  if (!body || typeof body !== "object") return false;
  const b = body as Record<string, unknown>;
  return b.error === "step_up_required" && typeof b.redirect === "string";
}

export const http = {
  get: <T>(path: string, params?: Record<string, string>) =>
    request<T>(path, { method: "GET", params }),

  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body ? JSON.stringify(body) : undefined }),

  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PUT", body: body ? JSON.stringify(body) : undefined }),

  delete: <T>(path: string, params?: Record<string, string>) =>
    request<T>(path, { method: "DELETE", params }),
};

export { HttpError };
