-- Phase 3: the secure secret drop. See docs/SECRETS.md.
--
-- Only ciphertext, nonce, wrapped data key, an HMAC fingerprint, the last four
-- characters and metadata are stored. Plaintext values never touch the
-- database. room_id is a plain text namespace (durable room id, or "guild" for
-- the owner-cookie chat), so it carries no foreign key.

create table if not exists guild_secrets (
  id text primary key check (id ~ '^s_[0-9a-f]{32}$'),
  room_id text not null check (room_id ~ '^[A-Za-z0-9_-]{1,64}$'),
  name text not null check (name ~ '^[A-Za-z_][A-Za-z0-9_]{0,63}$'),
  provider text not null check (provider ~ '^[a-z_]{1,32}$'),
  source_name text not null default '' check (length(source_name) <= 100),
  source_url text not null default '' check (source_url = '' or source_url ~ '^https://'),
  scopes jsonb not null default '[]'::jsonb check (jsonb_typeof(scopes) = 'array'),
  environments jsonb not null default '[]'::jsonb check (jsonb_typeof(environments) = 'array'),
  status text not null default 'unverified'
    check (status in ('unverified', 'verified', 'invalid', 'revoked')),
  last4 text not null default '' check (length(last4) <= 4),
  fingerprint text not null check (fingerprint ~ '^[0-9a-f]{64}$'),
  ciphertext bytea not null,
  nonce bytea not null check (length(nonce) = 12),
  wrapped_key bytea not null check (length(wrapped_key) = 60),
  key_version integer not null default 1,
  version integer not null default 1,
  created_by text not null,
  expires_at timestamptz,
  verified_at timestamptz,
  rotated_at timestamptz,
  revoked_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create unique index if not exists guild_secrets_live_name_idx
  on guild_secrets (room_id, name) where revoked_at is null;

create table if not exists guild_secret_grants (
  id text primary key check (id ~ '^g_[0-9a-f]{32}$'),
  secret_id text not null references guild_secrets(id) on delete cascade,
  agent_id text not null check (agent_id ~ '^[A-Za-z0-9_-]{1,64}$'),
  scopes jsonb not null default '[]'::jsonb check (jsonb_typeof(scopes) = 'array'),
  reveal boolean not null default false,
  expires_at timestamptz not null,
  max_uses integer check (max_uses is null or max_uses > 0),
  uses integer not null default 0 check (uses >= 0),
  granted_by text not null,
  created_at timestamptz not null default now(),
  revoked_at timestamptz,
  check (max_uses is null or uses <= max_uses)
);

create index if not exists guild_secret_grants_secret_idx
  on guild_secret_grants (secret_id, agent_id) where revoked_at is null;

-- Short-lived scoped handles. Only an HMAC digest of the handle is stored.
create table if not exists guild_secret_handles (
  digest text primary key check (digest ~ '^[0-9a-f]{64}$'),
  grant_id text not null references guild_secret_grants(id) on delete cascade,
  scope text not null default '',
  expires_at timestamptz not null,
  used_at timestamptz,
  created_at timestamptz not null default now()
);

-- One owner approval per sync. Approvals expire and are single use.
create table if not exists guild_secret_sync_approvals (
  id text primary key check (id ~ '^a_[0-9a-f]{32}$'),
  secret_id text not null references guild_secrets(id) on delete cascade,
  target text not null,
  environment text not null,
  plan_digest text not null check (plan_digest ~ '^[0-9a-f]{64}$'),
  approved_by text not null,
  expires_at timestamptz not null,
  consumed_at timestamptz,
  created_at timestamptz not null default now()
);

-- Append-only, hash-chained audit log. Never holds values.
create table if not exists guild_secret_audit (
  id bigint generated always as identity primary key,
  room_id text not null,
  secret_id text,
  actor text not null,
  action text not null,
  detail jsonb not null default '{}'::jsonb,
  prev_hash text not null default '',
  hash text not null check (hash ~ '^[0-9a-f]{64}$'),
  created_at timestamptz not null default now()
);

create index if not exists guild_secret_audit_room_idx on guild_secret_audit (room_id, id);

create or replace function guild_secret_audit_append_only() returns trigger
language plpgsql as $$
begin
  raise exception 'guild_secret_audit is append-only';
end;
$$;

drop trigger if exists guild_secret_audit_no_change on guild_secret_audit;
create trigger guild_secret_audit_no_change
  before update or delete on guild_secret_audit
  for each row execute function guild_secret_audit_append_only();
