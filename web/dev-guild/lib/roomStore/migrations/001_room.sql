-- Phase 1 durable room state. The migration runner owns
-- guild_schema_migrations; this file is applied once in a transaction.

create table if not exists guild_rooms (
  id text primary key,
  prefs jsonb not null default '{}'::jsonb,
  prefs_version integer not null default 0,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists guild_threads (
  room_id text not null references guild_rooms(id) on delete cascade,
  id text not null,
  title text not null,
  members jsonb not null default '[]'::jsonb,
  is_group boolean not null default false,
  created_at timestamptz not null default now(),
  primary key (room_id, id)
);

create table if not exists guild_messages (
  room_id text not null references guild_rooms(id) on delete cascade,
  seq bigint generated always as identity,
  id text not null,
  thread_id text not null,
  from_id text not null,
  kind text not null,
  body jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  primary key (room_id, seq),
  unique (room_id, id)
);

create index if not exists guild_messages_thread_seq_idx
  on guild_messages (room_id, thread_id, seq);

create table if not exists guild_participants (
  room_id text not null references guild_rooms(id) on delete cascade,
  id text not null,
  name text not null,
  kind text not null,
  persona text not null default '',
  created_at timestamptz not null default now(),
  last_seen_at timestamptz,
  revoked_at timestamptz,
  primary key (room_id, id)
);

create table if not exists guild_custom_agents (
  room_id text not null references guild_rooms(id) on delete cascade,
  id text not null,
  name text not null,
  metadata jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (room_id, id)
);

create table if not exists guild_reactions (
  room_id text not null references guild_rooms(id) on delete cascade,
  thread_id text not null,
  message_id text not null,
  emoji text not null,
  actor_id text not null,
  created_at timestamptz not null default now(),
  primary key (room_id, thread_id, message_id, emoji, actor_id),
  foreign key (room_id, message_id) references guild_messages (room_id, id) on delete cascade
);

create index if not exists guild_reactions_message_idx
  on guild_reactions (room_id, thread_id, message_id);
