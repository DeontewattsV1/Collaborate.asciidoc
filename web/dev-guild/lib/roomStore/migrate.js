import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";

export const MIGRATIONS_DIR = fileURLToPath(new URL("./migrations/", import.meta.url));

// Apply every .sql file once, in filename order, with one transaction per file.
// `client` is a pg Client (or a test double with the same query method).
export async function applyMigrations({ client, dir = MIGRATIONS_DIR } = {}) {
  if (!client || typeof client.query !== "function") {
    throw new Error("applyMigrations requires a database client");
  }
  await client.query(
    "create table if not exists guild_schema_migrations (filename text primary key, applied_at timestamptz not null default now())",
  );
  const files = readdirSync(dir)
    .filter((name) => name.endsWith(".sql"))
    .sort();
  const { rows } = await client.query("select filename from guild_schema_migrations");
  const applied = new Set((rows || []).map((row) => row.filename));
  const newlyApplied = [];

  for (const file of files) {
    if (applied.has(file)) continue;
    const sql = readFileSync(join(dir, file), "utf8");
    await client.query("begin");
    try {
      await client.query(sql);
      await client.query("insert into guild_schema_migrations (filename) values ($1)", [file]);
      await client.query("commit");
      newlyApplied.push(file);
    } catch (err) {
      await client.query("rollback").catch(() => {});
      throw err;
    }
  }

  return {
    applied: newlyApplied,
    skipped: files.filter((file) => applied.has(file)),
  };
}

export async function migrate({ connectionString, dir = MIGRATIONS_DIR } = {}) {
  if (!connectionString) throw new Error("migrate requires a connection string");
  const { default: pg } = await import("pg");
  const client = new pg.Client({ connectionString });
  await client.connect();
  try {
    return await applyMigrations({ client, dir });
  } finally {
    await client.end();
  }
}
