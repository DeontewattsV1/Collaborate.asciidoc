import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import {
  checkGuildAuth,
  cookieOptions,
  decryptCustomAgents,
  encryptCustomAgents,
  getOwnerToken,
  readGroupPrefs,
  sealGroupPrefs,
} from "../../../../lib/auth.js";
import {
  appendLine,
  buildCatalog,
  mergeCustomAgents,
  sanitizeCustomAgents,
  saveCustomAgent,
  slugifyAgentId,
  updateGroupMember,
} from "../../../../lib/store.js";

export const dynamic = "force-dynamic";

function validEndpointUrl(raw) {
  try {
    const u = new URL(String(raw || "").trim());
    if (u.protocol !== "https:" && u.protocol !== "http:") return "";
    const host = u.hostname.toLowerCase();
    if (host === "169.254.169.254" || host.endsWith(".internal")) return "";
    return u.toString().replace(/\/$/, "");
  } catch {
    return "";
  }
}

export async function GET(request) {
  const token = getOwnerToken();
  const jar = await cookies();
  const auth = checkGuildAuth({ jar, token, authHeader: request.headers.get("authorization") });
  if (!auth.ok) return NextResponse.json({ error: auth.error }, { status: auth.status });
  const cookieAgents = decryptCustomAgents(jar.get("guild_custom_agents")?.value || "", token);
  return NextResponse.json({ agents: sanitizeCustomAgents(mergeCustomAgents(cookieAgents)) });
}

export async function POST(request) {
  const token = getOwnerToken();
  const jar = await cookies();
  const auth = checkGuildAuth({ jar, token, authHeader: request.headers.get("authorization") });
  if (!auth.ok) return NextResponse.json({ error: auth.error }, { status: auth.status });

  let body = {};
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "bad json" }, { status: 400 });
  }

  const name = String(body.name || "").trim().slice(0, 40);
  if (!name) {
    return NextResponse.json({ error: "Agent name is required." }, { status: 400 });
  }
  const persona = String(body.persona || "").trim().slice(0, 800);
  const role = String(body.role || "").trim().slice(0, 80) || "Custom guild agent.";
  const catchphrase = String(body.catchphrase || "").trim().slice(0, 100) || "Ready when you are.";
  const endpointType = body.endpointType === "openai" ? "openai" : "gemini";
  const modelId = String(body.modelId || "").trim().slice(0, 100) || (endpointType === "gemini" ? "gemini-3.5-flash-lite" : "");
  const baseUrl = endpointType === "openai" ? validEndpointUrl(body.baseUrl) : "";
  const apiKey = endpointType === "openai" ? String(body.apiKey || "").trim().slice(0, 400) : "";

  if (endpointType === "openai" && (!baseUrl || !modelId || !apiKey)) {
    return NextResponse.json(
      { error: "OpenAI-compatible agents need a valid Base URL, Model ID, and API key." },
      { status: 400 },
    );
  }

  const cookieAgents = decryptCustomAgents(jar.get("guild_custom_agents")?.value || "", token);
  const existing = buildCatalog({ env: process.env, customAgents: cookieAgents });
  const existingIds = new Set(existing.map((c) => c.id));
  const id = slugifyAgentId(name, existingIds);

  const agent = {
    id,
    name,
    kind: "custom",
    color: "#38BDF8",
    avatar: "claude.png",
    look: "Custom chibi guild member.",
    voice: persona || `Helpful custom agent named ${name}.`,
    quirk: "Brings its own custom instructions into the room.",
    catchphrase,
    role,
    talks: "Replies directly to Deonte and collaborates with the rest of The Dev Guild.",
    expertise: persona || role,
    persona,
    endpointType,
    modelId,
    baseUrl,
    apiKey,
    hasKey: endpointType === "openai" ? Boolean(apiKey) : Boolean(process.env.GOOGLE_GENERATIVE_AI_API_KEY),
    enable: endpointType === "openai"
      ? `Custom OpenAI-compatible endpoint (${modelId}).`
      : "Uses server GOOGLE_GENERATIVE_AI_API_KEY (Free Gemini).",
  };

  saveCustomAgent(agent);
  const nextAgents = mergeCustomAgents([...cookieAgents, agent]);
  const cookiePrefs = readGroupPrefs(jar.get("guild_group_prefs")?.value || "", token);
  const nextPrefs = updateGroupMember(id, "add", cookiePrefs);
  const catalog = buildCatalog({ env: process.env, customAgents: nextAgents });
  const view = catalog.find((c) => c.id === id);

  appendLine({
    threadId: "guild",
    from: "system",
    kind: "system",
    text: `${name} (@${id}) was added to The Dev Guild.`,
    catalog,
  });

  const res = NextResponse.json({ ok: true, agent: view });
  res.cookies.set("guild_custom_agents", encryptCustomAgents(nextAgents, token), cookieOptions());
  res.cookies.set("guild_group_prefs", sealGroupPrefs(nextPrefs, token), cookieOptions());
  return res;
}
