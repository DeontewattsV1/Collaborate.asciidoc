import { memoryStore } from "./memoryStore.js";
import { fileStore } from "./fileStore.js";
import { postgresStore } from "./postgresStore.js";

export * from "./errors.js";
export { applyMigrations, migrate, MIGRATIONS_DIR } from "./migrate.js";
export { memoryStore, fileStore, postgresStore };

// lib/store.js (used by every route) selects Postgres directly when
// GUILD_STORE=postgres and otherwise keeps its in-memory store. This factory
// remains for scripts and tests that want a specific adapter.
export function createRoomStore(env = process.env, options = {}) {
  const kind = String(
    options.kind || env.GUILD_STORE || (String(env.GUILD_DEMO || "") === "1" ? "memory" : "file"),
  ).toLowerCase();

  if (kind === "memory") return memoryStore();
  if (kind === "file") return fileStore({ dir: options.dir || env.GUILD_DATA_DIR });
  if (kind === "postgres") {
    const connectionString = options.connectionString || env.DATABASE_URL;
    if (!options.pool && !connectionString) {
      throw new Error("GUILD_STORE=postgres requires DATABASE_URL");
    }
    return postgresStore({ connectionString, pool: options.pool });
  }
  throw new Error(`unknown GUILD_STORE "${kind}"`);
}
