// Validation for user-supplied model endpoint URLs (custom OpenAI-compatible
// agents). The server calls these URLs itself, so an unchecked value is a
// server-side request forgery (SSRF) primitive: it could reach loopback
// services, the cloud metadata endpoint, or hosts on the private network.
//
// Self-hosters who deliberately run a model on their own network can set
// GUILD_ALLOW_PRIVATE_ENDPOINTS=1. This check is lexical: it does not resolve
// DNS, so a public name that resolves to a private address is not caught.

const BLOCKED_HOST_SUFFIXES = [".localhost", ".local", ".internal", ".localdomain", ".home.arpa"];
const BLOCKED_HOSTS = new Set(["localhost", "localhost.localdomain", "metadata", "metadata.google.internal"]);

function ipv4Parts(host) {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(host);
  if (!m) return null;
  const parts = m.slice(1).map(Number);
  return parts.every((n) => n <= 255) ? parts : null;
}

function isPrivateIPv4([a, b]) {
  return (
    a === 0 || // "this" network
    a === 10 || // RFC 1918
    a === 127 || // loopback
    (a === 100 && b >= 64 && b <= 127) || // CGNAT
    (a === 169 && b === 254) || // link-local, cloud metadata
    (a === 172 && b >= 16 && b <= 31) || // RFC 1918
    (a === 192 && b === 168) || // RFC 1918
    (a === 192 && b === 0) || // IETF protocol assignments
    (a === 198 && (b === 18 || b === 19)) || // benchmarking
    a >= 224 // multicast and reserved
  );
}

function isPrivateIPv6(host) {
  const h = host.toLowerCase();
  if (h === "::" || h === "::1") return true;
  // IPv4-mapped / IPv4-compatible forms, e.g. ::ffff:7f00:1 or ::ffff:127.0.0.1
  const mapped = /^::(?:ffff:)?(?:(\d+\.\d+\.\d+\.\d+)|([0-9a-f]{1,4}):([0-9a-f]{1,4}))$/.exec(h);
  if (mapped) {
    if (mapped[1]) {
      const parts = ipv4Parts(mapped[1]);
      return !parts || isPrivateIPv4(parts);
    }
    const hi = parseInt(mapped[2], 16);
    return isPrivateIPv4([hi >> 8, hi & 0xff]);
  }
  return (
    /^f[cd][0-9a-f]{0,2}:/.test(h) || // unique local fc00::/7
    /^fe[89ab][0-9a-f]?:/.test(h) || // link-local fe80::/10
    /^ff[0-9a-f]{0,2}:/.test(h) // multicast
  );
}

export function isPrivateHost(hostname) {
  const host = String(hostname || "").toLowerCase().replace(/\.$/, "");
  if (!host) return true;
  if (BLOCKED_HOSTS.has(host)) return true;
  if (BLOCKED_HOST_SUFFIXES.some((suffix) => host.endsWith(suffix))) return true;
  if (host.startsWith("[") && host.endsWith("]")) return isPrivateIPv6(host.slice(1, -1));
  const v4 = ipv4Parts(host);
  if (v4) return isPrivateIPv4(v4);
  // WHATWG URL already normalizes decimal/hex/octal IPv4 forms to dotted quads;
  // anything numeric-looking that survived is not a valid public hostname.
  if (/^[0-9.]+$/.test(host) || /^0x/i.test(host)) return true;
  return false;
}

export function validEndpointUrl(raw, env) {
  const source = env || process.env;
  const allowPrivate = String(source.GUILD_ALLOW_PRIVATE_ENDPOINTS || "").trim() === "1";
  let u;
  try {
    u = new URL(String(raw || "").trim());
  } catch {
    return "";
  }
  if (u.protocol !== "https:" && u.protocol !== "http:") return "";
  if (u.username || u.password) return "";
  if (!allowPrivate && isPrivateHost(u.hostname)) return "";
  u.hash = "";
  return u.toString().replace(/\/$/, "");
}
