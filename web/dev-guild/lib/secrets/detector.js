// Secret detector shared by the browser composer and the server. Pure JS with
// no Node imports so the client bundle can use it too.
//
// detectSecrets(text) finds likely credentials; redactSecrets(text) replaces
// every finding with a placeholder that keeps only the provider and the last
// four characters. A raw secret must never reach message history, logs,
// search, push payloads or agent prompts; every write path runs this first.

export const PROVIDERS = Object.freeze({
  openai: "OpenAI",
  anthropic: "Anthropic",
  google: "Google / Gemini",
  github: "GitHub",
  cloudflare: "Cloudflare",
  aws: "AWS",
  stripe: "Stripe",
  slack: "Slack",
  fly: "Fly.io",
  netlify: "Netlify",
  groq: "Groq",
  private_key: "Private key",
  generic: "Generic secret",
});

// Ordered: the more specific pattern wins when two overlap.
const RULES = [
  { provider: "private_key", re: /-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----[\s\S]*?-----END (?:[A-Z]+ )?PRIVATE KEY-----/g },
  { provider: "anthropic", re: /\bsk-ant-(?:api|admin)\d{2}-[A-Za-z0-9_-]{32,}\b/g },
  { provider: "openai", re: /\bsk-(?:proj|svcacct|admin)-[A-Za-z0-9_-]{20,}\b/g },
  { provider: "openai", re: /\bsk-[A-Za-z0-9]{20}T3BlbkFJ[A-Za-z0-9]{20}\b/g },
  { provider: "openai", re: /\bsk-[A-Za-z0-9]{40,}\b/g },
  { provider: "groq", re: /\bgsk_[A-Za-z0-9]{40,}\b/g },
  { provider: "google", re: /\bAIza[0-9A-Za-z_-]{35}\b/g },
  { provider: "google", re: /\bya29\.[0-9A-Za-z_-]{20,}\b/g },
  { provider: "github", re: /\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b/g },
  { provider: "github", re: /\bgithub_pat_[A-Za-z0-9_]{50,}\b/g },
  { provider: "aws", re: /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/g },
  { provider: "stripe", re: /\b(?:sk|rk)_(?:live|test)_[0-9A-Za-z]{20,}\b/g },
  { provider: "slack", re: /\bxox[abposr]-[0-9A-Za-z-]{10,}\b/g },
  { provider: "slack", re: /https:\/\/hooks\.slack\.com\/services\/[A-Za-z0-9/_-]{20,}/g },
  { provider: "fly", re: /\bFlyV1 fm\d_[A-Za-z0-9_+/=,-]{20,}/g },
  { provider: "fly", re: /\bfo1_[A-Za-z0-9_-]{30,}\b/g },
  { provider: "netlify", re: /\bnfp_[A-Za-z0-9]{30,}\b/g },
  { provider: "cloudflare", re: /\b(?:cfat|cfut)_[A-Za-z0-9_-]{30,}\b/g },
  // key=value style assignments: AWS secret keys, Cloudflare tokens, etc.
  {
    provider: "generic",
    re: /\b([A-Z][A-Z0-9_]*(?:SECRET|TOKEN|API_KEY|APIKEY|PASSWORD|PASSWD|PRIVATE_KEY|ACCESS_KEY)[A-Z0-9_]*)\s*[:=]\s*["']?([^\s"']{12,})["']?/g,
    group: 2,
    nameGroup: 1,
  },
];

const MAX_SCAN = 64 * 1024;

// Shannon entropy in bits per character.
export function entropy(s) {
  if (!s) return 0;
  const counts = new Map();
  for (const ch of s) counts.set(ch, (counts.get(ch) || 0) + 1);
  let h = 0;
  for (const n of counts.values()) {
    const p = n / s.length;
    h -= p * Math.log2(p);
  }
  return h;
}

const CANDIDATE = /[A-Za-z0-9_+/=.-]{32,}/g;
const ALLOW = [
  /^[0-9a-f]{40}$/i, // git commit SHA
  /^[0-9a-f]{64}$/i, // sha256 digest, published in logs on purpose
  /^m_[0-9a-f-]{36}$/i, // message ids
  /^(?:https?:)?\/\//i,
];

function looksRandom(token) {
  if (ALLOW.some((re) => re.test(token))) return false;
  if (/^[a-z]+$/i.test(token) || /^\d+$/.test(token)) return false;
  const classes = [/[a-z]/, /[A-Z]/, /\d/].filter((re) => re.test(token)).length;
  return classes >= 3 && entropy(token) >= 4.0;
}

export function last4(value) {
  const s = String(value || "");
  return s.length >= 12 ? s.slice(-4) : "";
}

export function detectSecrets(input) {
  const text = String(input || "").slice(0, MAX_SCAN);
  const found = [];
  const taken = [];
  const overlaps = (a, b) => taken.some(([s, e]) => a < e && b > s);
  for (const rule of RULES) {
    rule.re.lastIndex = 0;
    let m;
    while ((m = rule.re.exec(text))) {
      const value = rule.group ? m[rule.group] : m[0];
      const start = rule.group ? m.index + m[0].lastIndexOf(value) : m.index;
      const end = start + value.length;
      if (rule.provider === "generic" && !looksRandom(value) && value.length < 20) continue;
      if (overlaps(start, end)) continue;
      taken.push([start, end]);
      found.push({ provider: rule.provider, start, end, value, last4: last4(value), hint: rule.nameGroup ? m[rule.nameGroup] : "" });
    }
  }
  CANDIDATE.lastIndex = 0;
  let m;
  while ((m = CANDIDATE.exec(text))) {
    const start = m.index;
    const end = start + m[0].length;
    if (overlaps(start, end) || !looksRandom(m[0])) continue;
    taken.push([start, end]);
    found.push({ provider: "generic", start, end, value: m[0], last4: last4(m[0]), hint: "" });
  }
  return found.sort((a, b) => a.start - b.start);
}

export function placeholder(finding) {
  const label = PROVIDERS[finding.provider] || PROVIDERS.generic;
  return `[secret removed: ${label}${finding.last4 ? ` …${finding.last4}` : ""}]`;
}

// redactSecrets returns the safe text plus findings WITHOUT raw values unless
// keepValues is set (only the vault capture path asks for them).
export function redactSecrets(input, { keepValues = false, replace = placeholder } = {}) {
  const text = String(input ?? "");
  const findings = detectSecrets(text);
  if (!findings.length) return { text, findings: [], redacted: false };
  let out = "";
  let at = 0;
  for (const f of findings) {
    out += text.slice(at, f.start) + replace(f);
    at = f.end;
  }
  // Anything past the scan window is dropped rather than passed through unscanned.
  out += text.length > MAX_SCAN ? "" : text.slice(at);
  return {
    text: out,
    redacted: true,
    findings: findings.map(({ value, ...rest }) => (keepValues ? { ...rest, value } : rest)),
  };
}

export function containsSecret(text) {
  return detectSecrets(text).length > 0;
}

export function guessProvider(value) {
  const hit = detectSecrets(value)[0];
  return hit ? hit.provider : "generic";
}
