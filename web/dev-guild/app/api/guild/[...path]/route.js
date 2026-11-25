import { generateText } from "ai";
import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import {
  checkGuildAuth,
  cookieOptions,
  decryptCustomAgents,
  getOwnerToken,
  readGroupPrefs,
  sealGroupPrefs,
} from "../../../../lib/auth.js";
import {
  completeWithProvider,
  MAX_TURNS,
  parseMentions,
  planSpeakers,
} from "../../../../lib/models.js";
import { languageModel } from "../../../../lib/providers.js";
import {
  appendLine,
  buildCatalog,
  getMessages,
  hasVmBackend,
  listThreads,
  openDMThread,
  updateGroupMember,
  vmFetch,
} from "../../../../lib/store.js";

export const dynamic = "force-dynamic";

async function tryVm(token, method, path, body) {
  if (!hasVmBackend()) return null;
  try {
    return await vmFetch(token, method, path, body);
  } catch {
    return null;
  }
}

async function handleProxy(request, { params }) {
  const token = getOwnerToken();
  const jar = await cookies();
  const auth = checkGuildAuth({
    jar,
    token,
    authHeader: request.headers.get("authorization"),
  });
  if (!auth.ok) {
    return NextResponse.json({ error: auth.error }, { status: auth.status });
  }
  const parts = (await params).path || [];
  if (!parts.every((part) => /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(part))) {
    return NextResponse.json({ error: "bad path" }, { status: 400 });
  }

  const joined = parts.join("/");
  const cookieAgents = decryptCustomAgents(jar.get("guild_custom_agents")?.value || "", token);
  const cookiePrefs = readGroupPrefs(jar.get("guild_group_prefs")?.value || "", token);

  // GET /api/guild/v1/characters
  if (request.method === "GET" && joined === "v1/characters") {
    const vm = await tryVm(token, "GET", "/v1/characters");
    const characters = buildCatalog({
      env: process.env,
      customAgents: cookieAgents,
      vmCharacters: vm && vm.characters,
      includeSecrets: false,
    });
    return NextResponse.json({ characters });
  }

  // GET /api/guild/v1/threads
  if (request.method === "GET" && joined === "v1/threads") {
    const vmChars = await tryVm(token, "GET", "/v1/characters");
    const vmThreads = await tryVm(token, "GET", "/v1/threads");
    const catalog = buildCatalog({
      env: process.env,
      customAgents: cookieAgents,
      vmCharacters: vmChars && vmChars.characters,
    });
    const threads = listThreads({
      catalog,
      cookiePrefs,
      vmThreads: vmThreads && vmThreads.threads,
    });
    return NextResponse.json({ threads });
  }

  // POST /api/guild/v1/threads (open 1:1 DM)
  if (request.method === "POST" && joined === "v1/threads") {
    let body = {};
    try {
      body = await request.json();
    } catch {
      return NextResponse.json({ error: "bad json" }, { status: 400 });
    }
    const vm = await tryVm(token, "POST", "/v1/threads", body);
    if (vm && vm.id) return NextResponse.json(vm);
    const catalog = buildCatalog({ env: process.env, customAgents: cookieAgents });
    try {
      const thread = openDMThread(String(body.with || ""), catalog);
      return NextResponse.json(thread);
    } catch (err) {
      return NextResponse.json({ error: err.message }, { status: err.status || 400 });
    }
  }

  // POST /api/guild/v1/group/members (add/remove character from group)
  if (request.method === "POST" && joined === "v1/group/members") {
    let body = {};
    try {
      body = await request.json();
    } catch {
      return NextResponse.json({ error: "bad json" }, { status: 400 });
    }
    const id = String(body.id || "").trim();
    const action = body.action === "remove" ? "remove" : "add";
    const catalog = buildCatalog({ env: process.env, customAgents: cookieAgents });
    const character = catalog.find((c) => c.id === id);
    if (!character) {
      return NextResponse.json({ error: "unknown character" }, { status: 400 });
    }
    const nextPrefs = updateGroupMember(id, action, cookiePrefs);
    const threads = listThreads({ catalog, cookiePrefs: nextPrefs });
    const guild = threads.find((t) => t.id === "guild");
    const res = NextResponse.json({ ok: true, members: guild ? guild.members : [] });
    res.cookies.set("guild_group_prefs", sealGroupPrefs(nextPrefs, token), cookieOptions());
    return res;
  }

  // GET /api/guild/v1/threads/:id/messages
  if (request.method === "GET" && parts.length === 4 && parts[0] === "v1" && parts[1] === "threads" && parts[3] === "messages") {
    const threadId = parts[2];
    const incoming = new URL(request.url);
    const after = incoming.searchParams.get("after") || "0";
    if (!/^[0-9]{1,12}$/.test(after)) {
      return NextResponse.json({ error: "bad path" }, { status: 400 });
    }
    const vm = await tryVm(token, "GET", `/v1/threads/${threadId}/messages?after=${after}`);
    if (vm && Array.isArray(vm.messages)) {
      return NextResponse.json(vm);
    }
    return NextResponse.json({ messages: getMessages(threadId, Number(after)) });
  }

  // POST /api/guild/v1/threads/:id/messages
  if (request.method === "POST" && parts.length === 4 && parts[0] === "v1" && parts[1] === "threads" && parts[3] === "messages") {
    let body = {};
    try {
      body = await request.json();
    } catch {
      return NextResponse.json({ error: "bad json" }, { status: 400 });
    }
    const threadId = parts[2];
    const text = String(body.text || "").trim();
    if (!text) return NextResponse.json({ error: "message is empty" }, { status: 400 });

    const catalog = buildCatalog({ env: process.env, customAgents: cookieAgents, includeSecrets: true });
    const threads = listThreads({ catalog, cookiePrefs });
    const thread = threads.find((t) => t.id === threadId) || {
      id: threadId,
      group: threadId === "guild",
      members: catalog.map((c) => c.id),
    };

    const senderId = auth.role === "owner" ? "owner" : (auth.participant && auth.participant.id) || "guest";
    const senderName = auth.role === "owner" ? "Deonte" : (auth.participant && auth.participant.name) || "Guest";
    const posted = appendLine({
      threadId,
      from: senderId,
      senderName,
      text,
      catalog,
    });
    await tryVm(token, "POST", `/v1/threads/${threadId}/messages`, { text, reply: "hold" });

    const known = new Set(catalog.map((c) => c.id));
    const activeIds = new Set(catalog.filter((c) => c.online).map((c) => c.id));
    const byId = new Map(catalog.map((c) => [c.id, c]));
    const customPriority = catalog.filter((c) => c.kind === "custom" && c.online).slice(-1).map((c) => c.id);

    const queue = planSpeakers({
      members: thread.members,
      group: Boolean(thread.group),
      text,
      known,
      activeIds,
      priorityIds: customPriority,
    });

    const replies = [];
    const seen = new Set([senderId]);
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
        prompt: text,
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
          threadId,
          from: id,
          text: result.text,
          reasoning: result.summary || "",
          catalog,
        });
        replies.push(msg);
        await tryVm(token, "POST", `/v1/threads/${threadId}/lines`, {
          from: id,
          kind: "message",
          text: result.text,
          reasoning: result.summary || "",
        });
        if (thread.group) {
          for (const mention of parseMentions(result.text, known)) {
            if (!seen.has(mention)) queue.push(mention);
          }
        }
      }
    }

    return NextResponse.json({
      pending: false,
      via: replies.length ? "gemini" : "standalone",
      message: posted,
      replies,
    });
  }

  // Fallback VM proxy for any other /v1/* path when VM is configured
  const vm = await tryVm(
    token,
    request.method,
    `/${joined}`,
    request.method !== "GET" && request.method !== "HEAD" ? await request.json().catch(() => ({})) : undefined,
  );
  if (vm) return NextResponse.json(vm);
  return NextResponse.json({ error: "Not found in standalone mode." }, { status: 404 });
}

export const GET = handleProxy;
export const POST = handleProxy;
