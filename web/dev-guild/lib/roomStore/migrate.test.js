import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { applyMigrations } from "./migrate.js";

class FakeClient {
  constructor() {
    this.applied = [];
    this.statements = [];
    this.failOn = "";
  }

  async query(sql, params = []) {
    const text = String(sql).trim();
    this.statements.push(text);
    if (/^select filename from guild_schema_migrations/i.test(text)) {
      return { rows: this.applied.map((filename) => ({ filename })) };
    }
    if (/^insert into guild_schema_migrations/i.test(text)) {
      this.applied.push(params[0]);
      return { rows: [] };
    }
    if (this.failOn && text.includes(this.failOn)) {
      throw new Error("migration failed");
    }
    return { rows: [] };
  }
}

async function migrationsFixture(t) {
  const dir = await mkdtemp(join(tmpdir(), "guild-migrations-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  await writeFile(join(dir, "002_second.sql"), "create table two (id int);");
  await writeFile(join(dir, "001_first.sql"), "create table one (id int);");
  return dir;
}

test("applies migrations once, in filename order, and skips them on rerun", async (t) => {
  const dir = await migrationsFixture(t);
  const client = new FakeClient();

  const first = await applyMigrations({ client, dir });
  assert.deepEqual(first.applied, ["001_first.sql", "002_second.sql"]);
  assert.deepEqual(
    client.statements.filter((sql) => sql.startsWith("create table one") || sql.startsWith("create table two")),
    ["create table one (id int);", "create table two (id int);"],
  );

  const second = await applyMigrations({ client, dir });
  assert.deepEqual(second.applied, []);
  assert.deepEqual(second.skipped, ["001_first.sql", "002_second.sql"]);
});

test("rolls back and records nothing when a migration fails", async (t) => {
  const dir = await migrationsFixture(t);
  const client = new FakeClient();
  client.failOn = "create table two";

  await assert.rejects(() => applyMigrations({ client, dir }), /migration failed/);
  assert.deepEqual(client.applied, ["001_first.sql"]);
  assert.ok(client.statements.includes("rollback"));
});
