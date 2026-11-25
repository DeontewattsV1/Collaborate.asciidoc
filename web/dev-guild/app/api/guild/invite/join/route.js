import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import {
  cookieOptions,
  decryptCustomAgents,
  getOwnerToken,
  isOwnerCookie,
  isValidInviteCode,
  readGroupPrefs,
  sealGroupPrefs,
  sealInviteSession,
} from "../../../../../lib/auth.js";
import {
  appendLine,
  buildCatalog,
  getMessages,
  mergeGroupPrefs,
  registerInvitedParticipant,
  slugifyAgentId,
  updateGroupMember,
} from "../../../../../lib/store.js";

export const dynamic = "force-dynamic";

import { isDemoMode } from "../../../../../lib/demo.js";

export async function POST(request) {
  if (isDemoMode()) {
    return NextResponse.json({ error: "Invites are disabled in public demo mode." }, { status: 401 });
  }
  const token = getOwnerToken();
  if (!token) return NextResponse.json({ error: "Server access passphrase unset." }, { status: 503 });
  let body = {};
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "bad json" }, { status: 400 });
  }
  const jar = await cookies();
  const isOwner = isOwnerCookie(jar.get("guild_session")?.value || "", token);
  const bearer = String(request.headers.get("authorization") || "").replace(/^Bearer\s+/i, "").trim();
  const invite = String(body.invite || bearer || "").trim();
  if (!isOwner && !isValidInviteCode(invite, token)) {
    return NextResponse.json({ error: "Invalid invite code." }, { status: 401 });
  }

  const name = String(body.name || "").trim().slice(0, 40) || "Guest Agent";
  const kind = body.kind === "person" ? "person" : "agent";
  const persona = String(body.persona || "").trim().slice(0, 400);
  const cookieAgents = decryptCustomAgents(jar.get("guild_custom_agents")?.value || "", token);
  const cookiePrefs = readGroupPrefs(jar.get("guild_group_prefs")?.value || "", token);
  const prefs = mergeGroupPrefs(cookiePrefs);
  const existing = buildCatalog({ env: process.env, customAgents: cookieAgents, groupTitle: prefs.title });
  const id = slugifyAgentId(name, new Set(existing.map((c) => c.id)));

  const participant = {
    id,
    name,
    kind: kind === "person" ? "agent" : "custom",
    color: "#60A5FA",
    avatar: "codex-cloud.png",
    look: "Invited guild guest.",
    voice: persona || `Invited participant in ${prefs.title}.`,
    quirk: "Joined via shareable invite.",
    catchphrase: "Glad to be in the room.",
    role: kind === "person" ? "Invited guest." : "Invited external agent.",
    talks: "Collaborates with the owner and the group.",
    expertise: persona || "External collaborator.",
    persona,
    endpointType: "gemini",
    modelId: "gemini-3.5-flash-lite",
    invited: true,
    code: invite,
  };

  registerInvitedParticipant(participant);
  const nextPrefs = updateGroupMember(id, "add", cookiePrefs);
  const catalog = buildCatalog({ env: process.env, customAgents: cookieAgents, groupTitle: nextPrefs.title });
  const joinMsg = appendLine({
    threadId: "guild",
    from: "system",
    kind: "system",
    text: `${name} (@${id}) joined ${nextPrefs.title} via invite.`,
    catalog,
  });

  const res = NextResponse.json({
    ok: true,
    id,
    name,
    thread: "guild",
    groupTitle: nextPrefs.title,
    joinMessage: joinMsg,
    messages: getMessages("guild", 0),
    endpoints: {
      postMessage: "/api/guild/invite/message",
      listMessages: `/api/guild/invite/message?invite=${encodeURIComponent(invite)}`,
    },
  });
  res.cookies.set("guild_invite", sealInviteSession({ code: invite, id, name, kind }, token), cookieOptions());
  res.cookies.set("guild_group_prefs", sealGroupPrefs(nextPrefs, token), cookieOptions());
  return res;
}
