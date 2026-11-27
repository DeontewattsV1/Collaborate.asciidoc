-- Phase 2: individual identities, per-room roles, scanned attachment metadata
-- and idempotent sends. Applied once in a transaction by the migration runner.
--
-- Identities are people or agents that authenticate with an opaque 256-bit
-- credential. Only the SHA-256 hash of the credential is stored. AI personas
-- (guild_participants, guild_custom_agents) are display records and are never
-- identities, so adding one can never grant room permissions.

alter table guild_participants
  add column if not exists profile jsonb not null default '{}'::jsonb;

create table if not exists guild_identities (
  id text primary key check (id ~ '^[A-Za-z0-9_-]{1,64}$'),
  display_name text not null default '',
  credential_hash text not null unique check (credential_hash ~ '^[0-9a-f]{64}$'),
  created_at timestamptz not null default now(),
  revoked_at timestamptz
);

create table if not exists guild_room_memberships (
  room_id text not null references guild_rooms(id) on delete cascade,
  identity_id text not null references guild_identities(id) on delete cascade,
  role text not null check (role in ('owner', 'member', 'viewer')),
  granted_by text references guild_identities(id),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  revoked_at timestamptz,
  primary key (room_id, identity_id)
);

create index if not exists guild_room_memberships_identity_idx
  on guild_room_memberships (identity_id) where revoked_at is null;

create table if not exists guild_attachments (
  room_id text not null references guild_rooms(id) on delete cascade,
  id text not null check (id ~ '^[0-9a-f]{32}$'),
  uploader_id text not null references guild_identities(id),
  object_key text not null unique,
  name text not null,
  sha256 text not null check (sha256 ~ '^[0-9a-f]{64}$'),
  size_bytes bigint not null check (size_bytes > 0),
  mime text not null default 'application/octet-stream',
  scan_status text not null check (scan_status in ('clean', 'rejected')),
  scanned_at timestamptz not null default now(),
  created_at timestamptz not null default now(),
  primary key (room_id, id),
  -- The object key must live under this room's prefix.
  check (object_key like 'rooms/' || room_id || '/%')
);

create table if not exists guild_send_idempotency (
  room_id text not null references guild_rooms(id) on delete cascade,
  identity_id text not null references guild_identities(id) on delete cascade,
  idem_key text not null check (length(idem_key) between 1 and 128),
  message_id text,
  created_at timestamptz not null default now(),
  primary key (room_id, identity_id, idem_key)
);
