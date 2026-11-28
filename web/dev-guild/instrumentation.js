// Next.js calls register() once per server process before serving requests.
// Scrub likely credentials from every console line (see docs/SECRETS.md).
export async function register() {
  if (process.env.NEXT_RUNTIME === "edge") return;
  const { installLogRedaction } = await import("./lib/secrets/guard.js");
  installLogRedaction();
}
