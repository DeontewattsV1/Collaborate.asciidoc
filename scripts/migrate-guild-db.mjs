#!/usr/bin/env node
import { migrate, MIGRATIONS_DIR } from "../web/dev-guild/lib/roomStore/migrate.js";

const args = process.argv.slice(2);
let connectionString = process.env.DATABASE_URL || "";
let dir = MIGRATIONS_DIR;

for (let i = 0; i < args.length; i += 1) {
  const arg = args[i];
  if (arg === "--database-url") connectionString = args[++i] || "";
  else if (arg === "--dir") dir = args[++i] || dir;
  else if (arg === "--help" || arg === "-h") {
    console.log("usage: node scripts/migrate-guild-db.mjs [--database-url URL] [--dir DIR]");
    process.exit(0);
  } else {
    console.error(`unknown argument: ${arg}`);
    process.exit(2);
  }
}

if (!connectionString) {
  console.error("DATABASE_URL is required (or pass --database-url)");
  process.exit(2);
}

try {
  const result = await migrate({ connectionString, dir });
  console.log(
    `migrations: ${result.applied.length} applied, ${result.skipped.length} already applied`,
  );
  for (const file of result.applied) console.log(`  applied ${file}`);
} catch (err) {
  console.error(`migration failed: ${err.message}`);
  process.exit(1);
}
