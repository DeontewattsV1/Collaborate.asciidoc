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
  checkDemoRateLimit,
  clientIpFromRequest,
  DEMO_MAX_INPUT_CHARS,
  DEMO_MAX_OUTPUT_TOKENS,
  DEMO_MAX_TURNS,
  isDemoMode,
} from "../../../../lib/demo.js";
import {
  completeWithProvider,
  MAX_TURNS,
  parseMentions,
  planSpeakers,
} from "../../../../lib/models.js";
import { languageModel } from "../../../../lib/providers.js";
import { viewerIdFromAuth } from "../../../../lib/social.js";
import {
  addReaction,
  appendLine,
  buildCatalog,
  configureGroup,
  getMessages,
  hasVmBackend,
  listThreads,
  mergeGroupPrefs,
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
  const demo = isDemoMode();
  const cookieAgents = demo ? [] : decryptCustomAgents(jar.get("guild_custom_agents")?.value || "", token);
  const cookiePrefs = demo ? null : readGroupPrefs(jar.get("guild_group_prefs")?.value || "", token);
  const prefs = await mergeGroupPrefs(cookiePrefs);

  // GET /api/guild/v1/characters
  if (request.method === "GET" && joined === "v1/characters") {
    const vm = await tryVm(token, "GET", "/v1/characters");
    const characters = await buildCatalog({
      env: process.env,
      customAgents: cookieAgents,
      vmCharacters: vm && vm.characters,
      includeSecrets: false,
      includeDirectory: true,
      groupTitle: prefs.title,
    });
    return NextResponse.json({ characters });
  }

  // GET /api/guild/v1/threads
  if (request.method === "GET" && joined === "v1/threads") {
    const vmChars = await tryVm(token, "GET", "/v1/characters");
    const vmThreads = await tryVm(token, "GET", "/v1/threads");
    const catalog = await buildCatalog({
      env: process.env,
      customAgents: cookieAgents,
      vmCharacters: vmChars && vmChars.characters,
      includeDirectory: true,
      groupTitle: prefs.title,
    });
    const threads = await listThreads({
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
    const catalog = await buildCatalog({ env: process.env, customAgents: cookieAgents, includeDirectory: true, groupTitle: prefs.title });
    try {
      const thread = await openDMThread(String(body.with || ""), catalog);
      return NextResponse.json(thread);
    } catch (err) {
      return NextResponse.json({ error: err.message }, { status: err.status || 400 });
    }
  }

  // POST /api/guild/v1/group (create/configure or rename group chat)
  if (request.method === "POST" && joined === "v1/group") {
    if (demo) {
      return NextResponse.json({ error: "Group settings are disabled in public demo mode." }, { status: 401 });
    }
    if (auth.role !== "owner") {
      return NextResponse.json({ error: "Only the group owner can update group settings." }, { status: 403 });
    }
    let body = {};
    try {
      body = await request.json();
    } catch {
      return NextResponse.json({ error: "bad json" }, { status: 400 });
    }
    const catalog = await buildCatalog({ env: process.env, customAgents: cookieAgents, includeDirectory: true, groupTitle: prefs.title });
    const wasSetupDone = Boolean(prefs.setupDone);
    const nextPrefs = await configureGroup(
      {
        title: body.title,
        ownerName: body.ownerName,
        members: Array.isArray(body.members) ? body.members : undefined,
        setupDone: body.setupDone !== undefined ? Boolean(body.setupDone) : true,
        replaceMembers: Boolean(body.replaceMembers),
      },
      cookiePrefs,
      catalog,
    );
    const threads = await listThreads({ catalog, cookiePrefs: nextPrefs });
    const guild = threads.find((t) => t.id === "guild");
    const memberCount = guild ? Math.max(guild.members.length - 1, 0) : 0;
    if (!wasSetupDone && nextPrefs.setupDone) {
      await appendLine({
        threadId: "guild",
        from: "system",
        kind: "system",
        text: `${nextPrefs.ownerName} created "${nextPrefs.title}"${memberCount ? ` with ${memberCount} member${memberCount === 1 ? "" : "s"}.` : ". Tap + Add / Invite to pick members."}`,
        catalog,
      });
    }
    const res = NextResponse.json({
      ok: true,
      group: {
        title: nextPrefs.title,
        ownerName: nextPrefs.ownerName,
        setupDone: nextPrefs.setupDone,
        members: guild ? guild.members : ["owner"],
      },
    });
    res.cookies.set("guild_group_prefs", sealGroupPrefs(nextPrefs, token), cookieOptions());
    return res;
  }

  // POST /api/guild/v1/group/members (add/remove character from group)
  if (request.method === "POST" && joined === "v1/group/members") {
    if (demo) {
      return NextResponse.json({ error: "Roster changes are disabled in public demo mode." }, { status: 401 });
    }
    let body = {};
    try {
      body = await request.json();
    } catch {
      return NextResponse.json({ error: "bad json" }, { status: 400 });
    }
    const id = String(body.id || "").trim();
    const action = body.action === "remove" ? "remove" : "add";
    const catalog = await buildCatalog({ env: process.env, customAgents: cookieAgents, includeDirectory: true, groupTitle: prefs.title });
    const character = catalog.find((c) => c.id === id);
    if (!character) {
      return NextResponse.json({ error: "unknown character" }, { status: 400 });
    }
    const nextPrefs = await updateGroupMember(id, action, cookiePrefs);
    const threads = await listThreads({ catalog, cookiePrefs: nextPrefs });
    const guild = threads.find((t) => t.id === "guild");
    const res = NextResponse.json({ ok: true, members: guild ? guild.members : [] });
    res.cookies.set("guild_group_prefs", sealGroupPrefs(nextPrefs, token), cookieOptions());
    return res;
  }

  // POST /api/guild/v1/threads/:id/messages/:messageId/reactions
  if (
    request.method === "POST" &&
    parts.length === 6 &&
    parts[0] === "v1" &&
    parts[1] === "threads" &&
    parts[3] === "messages" &&
    parts[5] === "reactions"
  ) {
    let body = {};
    try {
      body = await request.json();
    } catch {
      return NextResponse.json({ error: "bad json" }, { status: 400 });
    }
    const threadId = parts[2];
    const messageId = parts[4];
    if (!/^m_[A-Za-z0-9_-]{1,64}$/.test(messageId)) {
      return NextResponse.json({ error: "bad message id" }, { status: 400 });
    }
    const from = auth.role === "owner" ? "owner" : (auth.participant && auth.participant.id) || "guest";
    try {
      const result = await addReaction({ threadId, messageId, emoji: body.emoji, from, allowMissing: hasVmBackend() });
      return NextResponse.json({ ok: true, ...result });
    } catch (err) {
      return NextResponse.json({ error: err.message }, { status: err.status || 400 });
    }
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
    return NextResponse.json({
      messages: await getMessages(threadId, Number(after), undefined, viewerIdFromAuth(auth)),
    });
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
    const rawText = String(body.text || "").trim();
    const text = demo ? rawText.slice(0, DEMO_MAX_INPUT_CHARS) : rawText;
    if (!text) return NextResponse.json({ error: "message is empty" }, { status: 400 });
    let rateInfo = null;
    if (demo) {
      rateInfo = checkDemoRateLimit(clientIpFromRequest(request), { cookieVal: jar.get("guild_demo_rl")?.value });
      if (!rateInfo.allowed) {
        return NextResponse.json(
          {
            error: "Demo rate limit reached (10 messages/hour). Deploy your own free copy from the README for unlimited chat!",
            retryAfterSeconds: rateInfo.retryAfterSeconds,
          },
          {
            status: 429,
            headers: {
              "Retry-After": String(rateInfo.retryAfterSeconds),
              "X-RateLimit-Limit": String(rateInfo.limit),
              "X-RateLimit-Remaining": "0",
            },
          },
        );
      }
    }

    const [vmChars, vmThreads] = demo
      ? [null, null]
      : await Promise.all([
          tryVm(token, "GET", "/v1/characters"),
          tryVm(token, "GET", "/v1/threads"),
        ]);
    const catalog = await buildCatalog({
      env: process.env,
      customAgents: cookieAgents,
      vmCharacters: vmChars && vmChars.characters,
      includeSecrets: true,
      includeDirectory: true,
      groupTitle: prefs.title,
    });
    const threads = await listThreads({
      catalog,
      cookiePrefs,
      vmThreads: vmThreads && vmThreads.threads,
    });
    const thread = threads.find((t) => t.id === threadId) || {
      id: threadId,
      group: threadId === "guild",
      members: ["owner"],
    };

    const senderId = (auth.role === "owner" || auth.role === "demo") ? "owner" : (auth.participant && auth.participant.id) || "guest";
    const senderName = auth.role === "demo"
      ? "You"
      : auth.role === "owner"
        ? prefs.ownerName
        : (auth.participant && auth.participant.name) || "Guest";
    const posted = await appendLine({
      threadId,
      from: senderId,
      senderName,
      text,
      catalog,
    });
    if (!demo) await tryVm(token, "POST", `/v1/threads/${threadId}/messages`, { text, reply: "hold" });

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

    const maxTurns = demo ? DEMO_MAX_TURNS : MAX_TURNS;
    const replies = [];
    const seen = new Set([senderId]);
    let turns = 0;
    while (queue.length && turns < maxTurns) {
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
        maxOutputTokens: demo ? DEMO_MAX_OUTPUT_TOKENS : undefined,
        run: (req) =>
          generateText({
            model: languageModel(req),
            system: req.system,
            prompt: req.prompt,
            maxOutputTokens: req.maxOutputTokens,
            providerOptions: req.providerOptions,
            maxRetries: 0,
          }),
      });
      if (result && result.ok && result.text) {
        const msg = await appendLine({
          threadId,
          from: id,
          text: result.text,
          reasoning: result.summary || "",
          catalog,
        });
        replies.push(msg);
        if (!demo) {
          await tryVm(token, "POST", `/v1/threads/${threadId}/lines`, {
            from: id,
            kind: "message",
            text: result.text,
            reasoning: result.summary || "",
          });
        }
        if (thread.group) {
          for (const mention of parseMentions(result.text, known)) {
            if (!seen.has(mention)) queue.push(mention);
          }
        }
      }
    }

    return NextResponse.json(
      {
        pending: false,
        via: demo ? "demo-live" : (replies.length ? "gemini" : "standalone"),
        message: posted,
        replies,
        rateLimit: rateInfo ? { limit: rateInfo.limit, remaining: rateInfo.remaining } : undefined,
      },
      rateInfo
        ? {
            headers: {
              "X-RateLimit-Limit": String(rateInfo.limit),
              "X-RateLimit-Remaining": String(rateInfo.remaining),
            },
          }
        : undefined,
    );
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
