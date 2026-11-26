import { generateText } from "ai";
import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import {
  checkGuildAuth,
  decryptCustomAgents,
  getOwnerToken,
  isValidInviteCode,
  readGroupPrefs,
} from "../../../../../lib/auth.js";
import { completeWithProvider, MAX_TURNS, parseMentions, planSpeakers } from "../../../../../lib/models.js";
import { languageModel } from "../../../../../lib/providers.js";
import {
  appendLine,
  buildCatalog,
  invitedMessages,
  mergeGroupPrefs,
  resolveGroupMembers,
} from "../../../../../lib/store.js";

export const dynamic = "force-dynamic";

import { isDemoMode } from "../../../../../lib/demo.js";

export async function GET(request) {
  if (isDemoMode()) {
    return NextResponse.json({ error: "Invites are disabled in public demo mode." }, { status: 401 });
  }
  const token = getOwnerToken();
  const jar = await cookies();
  const url = new URL(request.url);
  const inviteParam = url.searchParams.get("invite") || "";
  const after = Number(url.searchParams.get("after") || 0);
  const auth = checkGuildAuth({
    jar,
    token,
    inviteParam,
    authHeader: request.headers.get("authorization"),
  });
  if (!auth.ok) return NextResponse.json({ error: auth.error }, { status: auth.status });
  return NextResponse.json({ thread: "guild", messages: invitedMessages({ auth, after }) });
}

export async function POST(request) {
  if (isDemoMode()) {
    return NextResponse.json({ error: "Invites are disabled in public demo mode." }, { status: 401 });
  }
  const token = getOwnerToken();
  const jar = await cookies();
  let body = {};
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "bad json" }, { status: 400 });
  }
  const auth = checkGuildAuth({
    jar,
    token,
    inviteParam: body.invite || "",
    authHeader: request.headers.get("authorization"),
  });
  if (!auth.ok && !isValidInviteCode(body.invite || "", token)) {
    return NextResponse.json({ error: auth.error || "Invalid invite token." }, { status: 401 });
  }

  const text = String(body.text || "").trim().slice(0, 4000);
  if (!text) return NextResponse.json({ error: "text is required" }, { status: 400 });

  const cookieAgents = decryptCustomAgents(jar.get("guild_custom_agents")?.value || "", token);
  const cookiePrefs = readGroupPrefs(jar.get("guild_group_prefs")?.value || "", token);
  const prefs = mergeGroupPrefs(cookiePrefs);
  const catalog = buildCatalog({
    env: process.env,
    customAgents: cookieAgents,
    includeSecrets: true,
    groupTitle: prefs.title,
  });
  const members = resolveGroupMembers({ catalog, cookiePrefs });
  const from = String(body.from || (auth.participant && auth.participant.id) || "guest").trim().slice(0, 40);
  const senderName = String(body.name || (auth.participant && auth.participant.name) || from).trim().slice(0, 40);

  const posted = appendLine({
    threadId: "guild",
    from,
    senderName,
    text,
    catalog,
  });

  const replies = [];
  if (body.reply !== false) {
    const known = new Set(catalog.map((c) => c.id));
    const activeIds = new Set(catalog.filter((c) => c.online).map((c) => c.id));
    const byId = new Map(catalog.map((c) => [c.id, c]));
    const queue = planSpeakers({
      members,
      group: true,
      text,
      known,
      activeIds,
    });
    const seen = new Set([from]);
    let turns = 0;
    while (queue.length && turns < MAX_TURNS) {
      const id = queue.shift();
      if (!id || seen.has(id)) continue;
      seen.add(id);
      const character = byId.get(id);
      if (!character || !character.online) continue;
      turns += 1;
      const result = await completeWithProvider({
        env: process.env,
        character,
        system: character.system,
        prompt: `${senderName}: ${text}`,
        run: (req) =>
          generateText({
            model: languageModel(req),
            system: req.system,
            prompt: req.prompt,
            providerOptions: req.providerOptions,
            maxRetries: 0,
          }),
      });
      if (result && result.ok && result.text) {
        const msg = appendLine({
          threadId: "guild",
          from: id,
          text: result.text,
          reasoning: result.summary || "",
          catalog,
        });
        replies.push(msg);
        for (const m of parseMentions(result.text, known)) {
          if (!seen.has(m)) queue.push(m);
        }
      }
    }
  }

  return NextResponse.json({ ok: true, message: posted, replies });
}
