#!/usr/bin/env node
// Operator-only: create an identity and print its credential ONCE.
// The credential is shown only here; the database stores its SHA-256 hash.
// Never paste the printed credential into chat, PRs, logs or commits.
//
//   DATABASE_URL=... node scripts/provision-identity.mjs <id> [--name "Display"] [--owner-of <roomId>]
//
// --owner-of makes the new identity the owner of an existing room (for example
// "guild", the room the web UI uses) so it can grant member/viewer roles.
import pg from "pg";
import { provisionIdentity } from "../lib/rooms/authz.js";

const args = process.argv.slice(2);
const id = args[0];
let name = "";
let ownerOf = "";
for (let i = 1; i < args.length; i += 1) {
  if (args[i] === "--name") name = args[++i] || "";
  else if (args[i] === "--owner-of") ownerOf = args[++i] || "";
  else {
    console.error(`unknown argument: ${args[i]}`);
    process.exit(2);
  }
}
if (!id || !process.env.DATABASE_URL) {
  console.error("usage: DATABASE_URL=... node scripts/provision-identity.mjs <id> [--name NAME] [--owner-of ROOM]");
  process.exit(2);
}

const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL, max: 1 });
try {
  const { token } = await provisionIdentity(pool, { id, displayName: name || id });
  if (ownerOf) {
    await pool.query("insert into guild_rooms (id) values ($1) on conflict (id) do nothing", [ownerOf]);
    await pool.query(
      `insert into guild_room_memberships (room_id, identity_id, role, granted_by)
       values ($1, $2, 'owner', $2)
       on conflict (room_id, identity_id) do update set role = 'owner', revoked_at = null, updated_at = now()`,
      [ownerOf, id],
    );
  }
  process.stderr.write(`identity ${id} created${ownerOf ? `, owner of ${ownerOf}` : ""}; credential follows on stdout\n`);
  process.stdout.write(`${token}\n`);
} finally {
  await pool.end();
}
