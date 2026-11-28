import { createHash } from "node:crypto";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";

// Sync targets push a vault secret into a deployment environment.
//
// Every sync is two steps: plan (dry run, owner only, returns a single-use
// approval that expires in 10 minutes) and apply (owner only, needs that
// approval AND SECRETS_SYNC_APPLY=on). Plans never contain values. Adapters
// take an injected fetch so tests make no network calls.

const pick = (env, base, environment) => String(env[`${base}_${environment.toUpperCase()}`] || env[base] || "").trim();

function need(values) {
  const missing = Object.entries(values).filter(([, v]) => !v).map(([k]) => k);
  if (missing.length) {
    const err = new Error(`sync target not configured: set ${missing.join(", ")}`);
    err.status = 503;
    throw err;
  }
}

async function ok(res, what) {
  if (!res.ok) {
    const err = new Error(`${what} answered ${res.status}`);
    err.status = 502;
    throw err;
  }
  return res;
}

export const SYNC_TARGETS = Object.freeze({
  fly: {
    label: "Fly.io app secrets",
    describe(env, environment) {
      return { app: pick(env, "SECRETS_SYNC_FLY_APP", environment), note: "Setting a Fly secret restarts the app's machines." };
    },
    async apply({ name, value, environment }, { env, fetchImpl }) {
      const app = pick(env, "SECRETS_SYNC_FLY_APP", environment);
      const token = String(env.SECRETS_SYNC_FLY_TOKEN || "").trim();
      need({ SECRETS_SYNC_FLY_APP: app, SECRETS_SYNC_FLY_TOKEN: token });
      const res = await fetchImpl("https://api.fly.io/graphql", {
        method: "POST",
        redirect: "error",
        headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
        body: JSON.stringify({
          query: "mutation($input: SetSecretsInput!) { setSecrets(input: $input) { app { name } } }",
          variables: { input: { appId: app, secrets: [{ key: name, value }] } },
        }),
      });
      await ok(res, "Fly");
      const body = await res.json().catch(() => ({}));
      if (body.errors && body.errors.length) {
        const err = new Error("Fly rejected the secret");
        err.status = 502;
        throw err;
      }
      return { destination: `fly:${app}` };
    },
  },
  github: {
    label: "GitHub Actions environment secrets",
    describe(env, environment) {
      const repo = pick(env, "SECRETS_SYNC_GITHUB_REPO", environment);
      return {
        repo,
        note: "Apply is not automated yet (GitHub needs a libsodium sealed box). Run the printed command; it reads the value from stdin.",
        command: `gh secret set <NAME> --repo ${repo || "<owner/repo>"} --env ${environment}`,
      };
    },
    async apply() {
      const err = new Error("GitHub sync is plan-only; use the gh command from the plan");
      err.status = 501;
      throw err;
    },
  },
  cloudflare: {
    label: "Cloudflare Worker secret",
    describe(env, environment) {
      return { account: pick(env, "SECRETS_SYNC_CF_ACCOUNT", environment), script: pick(env, "SECRETS_SYNC_CF_SCRIPT", environment) };
    },
    async apply({ name, value, environment }, { env, fetchImpl }) {
      const account = pick(env, "SECRETS_SYNC_CF_ACCOUNT", environment);
      const script = pick(env, "SECRETS_SYNC_CF_SCRIPT", environment);
      const token = String(env.SECRETS_SYNC_CF_TOKEN || "").trim();
      need({ SECRETS_SYNC_CF_ACCOUNT: account, SECRETS_SYNC_CF_SCRIPT: script, SECRETS_SYNC_CF_TOKEN: token });
      const url = `https://api.cloudflare.com/client/v4/accounts/${encodeURIComponent(account)}/workers/scripts/${encodeURIComponent(script)}/secrets`;
      await ok(await fetchImpl(url, {
        method: "PUT",
        redirect: "error",
        headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
        body: JSON.stringify({ name, text: value, type: "secret_text" }),
      }), "Cloudflare");
      return { destination: `cloudflare:${script}` };
    },
  },
  netlify: {
    label: "Netlify environment variable (secret)",
    describe(env, environment) {
      return { account: pick(env, "SECRETS_SYNC_NETLIFY_ACCOUNT", environment), site: pick(env, "SECRETS_SYNC_NETLIFY_SITE", environment), context: NETLIFY_CONTEXT[environment] };
    },
    async apply({ name, value, environment }, { env, fetchImpl }) {
      const account = pick(env, "SECRETS_SYNC_NETLIFY_ACCOUNT", environment);
      const site = pick(env, "SECRETS_SYNC_NETLIFY_SITE", environment);
      const token = String(env.SECRETS_SYNC_NETLIFY_TOKEN || "").trim();
      need({ SECRETS_SYNC_NETLIFY_ACCOUNT: account, SECRETS_SYNC_NETLIFY_SITE: site, SECRETS_SYNC_NETLIFY_TOKEN: token });
      const url = `https://api.netlify.com/api/v1/accounts/${encodeURIComponent(account)}/env?site_id=${encodeURIComponent(site)}`;
      await ok(await fetchImpl(url, {
        method: "POST",
        redirect: "error",
        headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
        body: JSON.stringify([{ key: name, is_secret: true, scopes: ["builds", "functions", "runtime"], values: [{ value, context: NETLIFY_CONTEXT[environment] }] }]),
      }), "Netlify");
      return { destination: `netlify:${site}` };
    },
  },
  dotenv: {
    label: "Local .env file (mode 0600)",
    describe(env, environment) {
      const dir = String(env.SECRETS_SYNC_DOTENV_DIR || "").trim();
      return { file: dir ? join(dir, `.env.${environment}`) : "", note: "Writes on the server's own disk only." };
    },
    async apply({ name, value, environment }, { env }) {
      const dir = String(env.SECRETS_SYNC_DOTENV_DIR || "").trim();
      need({ SECRETS_SYNC_DOTENV_DIR: dir });
      if (/[\r\n]/.test(value)) {
        const err = new Error("multi-line values cannot be written to a .env file");
        err.status = 400;
        throw err;
      }
      const root = resolve(dir);
      const file = join(root, `.env.${environment}`);
      await mkdir(root, { recursive: true, mode: 0o700 });
      const prior = await readFile(file, "utf8").catch(() => "");
      const lines = prior.split("\n").filter((l) => l && !l.startsWith(`${name}=`));
      lines.push(`${name}=${JSON.stringify(value)}`);
      const tmp = `${file}.${process.pid}.tmp`;
      await writeFile(tmp, `${lines.join("\n")}\n`, { mode: 0o600 });
      await rename(tmp, file);
      return { destination: `dotenv:${file}` };
    },
  },
});

const NETLIFY_CONTEXT = { dev: "dev", staging: "deploy-preview", prod: "production" };

export function syncApplyEnabled(env = process.env) {
  return String(env.SECRETS_SYNC_APPLY || "").toLowerCase() === "on";
}

export function buildPlan({ card, target, environment, env = process.env }) {
  const adapter = SYNC_TARGETS[target];
  if (!adapter) return null;
  const plan = {
    target,
    label: adapter.label,
    environment,
    name: card.name,
    secretId: card.id,
    version: card.version,
    last4: card.last4,
    dryRun: true,
    applyEnabled: syncApplyEnabled(env),
    ...adapter.describe(env, environment),
  };
  if (plan.command) plan.command = plan.command.replace("<NAME>", card.name);
  plan.digest = createHash("sha256").update(JSON.stringify([target, environment, card.id, card.version, card.name])).digest("hex");
  return plan;
}
