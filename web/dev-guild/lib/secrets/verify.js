// Provider verification: one cheap, read-only "who am I" call per provider.
//
// Off unless SECRETS_VERIFY=on. Tests inject a fake fetch. Responses are
// reduced to a status; bodies are never logged or stored, and redirects are
// refused so a key is never forwarded to another host.

const bearer = (v) => ({ authorization: `Bearer ${v}` });

export const VERIFIERS = Object.freeze({
  openai: (v) => ({ url: "https://api.openai.com/v1/models?limit=1", headers: bearer(v) }),
  groq: (v) => ({ url: "https://api.groq.com/openai/v1/models", headers: bearer(v) }),
  anthropic: (v) => ({ url: "https://api.anthropic.com/v1/models?limit=1", headers: { "x-api-key": v, "anthropic-version": "2023-06-01" } }),
  google: (v) => ({ url: "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1", headers: { "x-goog-api-key": v } }),
  github: (v) => ({ url: "https://api.github.com/user", headers: { ...bearer(v), accept: "application/vnd.github+json", "user-agent": "collaborate-secret-verify" } }),
  cloudflare: (v) => ({ url: "https://api.cloudflare.com/client/v4/user/tokens/verify", headers: bearer(v) }),
  stripe: (v) => ({ url: "https://api.stripe.com/v1/balance", headers: bearer(v) }),
  netlify: (v) => ({ url: "https://api.netlify.com/api/v1/user", headers: bearer(v) }),
  slack: (v) => ({ url: "https://slack.com/api/auth.test", method: "POST", headers: bearer(v), json: (body) => body && body.ok === true }),
  fly: (v) => ({ url: "https://api.fly.io/graphql", method: "POST", headers: { ...bearer(v), "content-type": "application/json" }, body: JSON.stringify({ query: "{ viewer { id } }" }), json: (body) => Boolean(body && body.data && body.data.viewer) }),
});

export function verificationEnabled(env = process.env) {
  return String(env.SECRETS_VERIFY || "").toLowerCase() === "on";
}

// Returns "verified", "invalid" or "unverified" (unsupported, disabled, or the
// provider could not be reached). Never throws with the value in the message.
export async function verifyValue(provider, value, { env = process.env, fetchImpl = globalThis.fetch, timeoutMs = 5000 } = {}) {
  if (!verificationEnabled(env)) return { status: "unverified", reason: "verification disabled" };
  const make = VERIFIERS[provider];
  if (!make) return { status: "unverified", reason: "no verifier for provider" };
  const spec = make(value);
  let res;
  try {
    res = await fetchImpl(spec.url, {
      method: spec.method || "GET",
      headers: spec.headers,
      body: spec.body,
      redirect: "error",
      cache: "no-store",
      signal: AbortSignal.timeout(timeoutMs),
    });
  } catch {
    return { status: "unverified", reason: "provider unreachable" };
  }
  if (res.status === 401 || res.status === 403) return { status: "invalid", reason: `provider answered ${res.status}` };
  if (!res.ok) return { status: "unverified", reason: `provider answered ${res.status}` };
  if (spec.json) {
    const body = await res.json().catch(() => null);
    return spec.json(body) ? { status: "verified", reason: "ok" } : { status: "invalid", reason: "provider rejected the credential" };
  }
  return { status: "verified", reason: "ok" };
}
