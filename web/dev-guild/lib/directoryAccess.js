// How each of the directory contacts can actually join a room.
//
// A directory entry is always a persona first: it can speak in character
// through the deployment's free Gemini key, clearly labelled as a persona and
// not the official vendor product. On top of that, each entry names the best
// way for the *real* thing to join:
//
//   persona      - only the in-character voice; used for products that are a
//                  service or a scanner rather than a conversational agent.
//   custom-agent - the vendor exposes a documented OpenAI-compatible chat
//                  endpoint, so its own API can back a named room agent.
//   invite       - the agent or runtime runs elsewhere and connects to the
//                  room through the owner-signed invite API.
//
// Every entry can also join through the invite API regardless of its mode.
// Only documented base URLs are listed. Per-account or per-project URLs
// (Azure OpenAI, Vertex AI, Amazon Bedrock) are deliberately absent: a wrong
// base URL is worse than an honest "persona or invite".
import directoryEntries from "../data/contacts.json" with { type: "json" };

export const JOIN_MODES = ["persona", "custom-agent", "invite"];
export const JOIN_DOCS = "docs/GUILD-DIRECTORY.md";

export const INVITE_ENDPOINTS = {
  join: "/api/guild/invite/join",
  message: "/api/guild/invite/message",
};

// Documented OpenAI-compatible chat-completions base URLs.
const OPENAI_COMPATIBLE = {
  "openai-api": "https://api.openai.com/v1",
  "groq": "https://api.groq.com/openai/v1",
  "openrouter-free": "https://openrouter.ai/api/v1",
  "google-ai-studio-gemini": "https://generativelanguage.googleapis.com/v1beta/openai/",
  "hugging-face-inference": "https://router.huggingface.co/v1",
  "ollama-2": "http://127.0.0.1:11434/v1",
  "xai-grok-api": "https://api.x.ai/v1",
  "mistral-la-plateforme": "https://api.mistral.ai/v1",
  "together-ai": "https://api.together.xyz/v1",
  "fireworks-ai": "https://api.fireworks.ai/inference/v1",
  "deepseek-api": "https://api.deepseek.com/v1",
  "perplexity-sonar": "https://api.perplexity.ai",
  "cerebras-inference": "https://api.cerebras.ai/v1",
  "sambanova-cloud": "https://api.sambanova.ai/v1",
  "github-models": "https://models.github.ai/inference",
  "nvidia-nim": "https://integrate.api.nvidia.com/v1",
  "deepinfra": "https://api.deepinfra.com/v1/openai",
  "hyperbolic": "https://api.hyperbolic.xyz/v1",
  "novita-ai": "https://api.novita.ai/v3/openai",
  "ai21-labs": "https://api.ai21.com/studio/v1",
  "moonshot-kimi-api": "https://api.moonshot.ai/v1",
  "zhipu-glm-api": "https://open.bigmodel.cn/api/paas/v4",
  "alibaba-qwen-dashscope": "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
  "cohere": "https://api.cohere.ai/compatibility/v1",
  "baseten": "https://inference.baseten.co/v1",
};

// Sections whose entries are agents or agent runtimes you can run yourself.
const INVITE_SECTIONS = new Set([
  "AI coding agents",
  "Agent frameworks",
  "Agent infra & browser automation",
]);

export function joinFor(entry) {
  const envKey = entry.requiredEnvKey || "GOOGLE_GENERATIVE_AI_API_KEY";
  const baseUrl = OPENAI_COMPATIBLE[entry.id];
  if (baseUrl) {
    return {
      mode: "custom-agent",
      label: `Add as a custom agent (${baseUrl})`,
      persona: true,
      invite: true,
      envKey,
      baseUrl,
      modelId: "",
      docs: `${JOIN_DOCS}#custom-agent`,
    };
  }
  if (INVITE_SECTIONS.has(entry.section)) {
    return {
      mode: "invite",
      label: "Run it outside the room and connect it with an invite",
      persona: true,
      invite: true,
      envKey,
      baseUrl: "",
      modelId: "",
      docs: `${JOIN_DOCS}#invite`,
    };
  }
  return {
    mode: "persona",
    label: "Chat now as a free Gemini persona",
    persona: true,
    invite: true,
    envKey,
    baseUrl: "",
    modelId: "",
    docs: `${JOIN_DOCS}#persona`,
  };
}

export function directoryIndex() {
  const sections = [];
  const bySection = new Map();
  for (const entry of directoryEntries) {
    const join = joinFor(entry);
    if (!bySection.has(entry.section)) {
      const bucket = { section: entry.section, count: 0, contacts: [] };
      bySection.set(entry.section, bucket);
      sections.push(bucket);
    }
    const bucket = bySection.get(entry.section);
    bucket.count += 1;
    bucket.contacts.push({
      id: entry.id,
      name: entry.name,
      section: entry.section,
      persona: entry.persona,
      mode: join.mode,
      envKey: join.envKey,
      baseUrl: join.baseUrl,
      docs: join.docs,
    });
  }
  const modeCounts = Object.fromEntries(JOIN_MODES.map((mode) => [mode, 0]));
  for (const section of sections) {
    for (const contact of section.contacts) modeCounts[contact.mode] += 1;
  }
  return {
    endpoint: "/api/guild/directory",
    count: directoryEntries.length,
    sectionCount: sections.length,
    modeCounts,
    invite: INVITE_ENDPOINTS,
    sections,
    contacts: sections.flatMap((section) => section.contacts),
  };
}

function configCell(contact) {
  if (contact.mode === "custom-agent") {
    return `\`${contact.envKey}\` · \`${contact.baseUrl}\``;
  }
  if (contact.mode === "invite") {
    return `\`POST ${INVITE_ENDPOINTS.join}\` · run the product with its own credentials`;
  }
  return "Free Gemini persona · no per-contact key";
}

export function renderDirectoryMarkdown() {
  const index = directoryIndex();
  const { persona, "custom-agent": customAgent, invite } = index.modeCounts;
  const lines = [
    "# Guild directory: how all 451 contacts join the room",
    "",
    "Every contact in [`web/dev-guild/data/contacts.json`](../web/dev-guild/data/contacts.json) can take part in a Dev Guild room.",
    "This file is generated by `node scripts/generate-guild-directory.mjs`; edit the JSON and rerun the script instead of editing this page.",
    "",
    `\`GET /api/guild/directory\` returns the same data as JSON, without an owner token and without any secret values. It reports ${index.count} contacts across ${index.sectionCount} sections.`,
    "",
    "## The three ways in",
    "",
    "| Mode | Contacts | What joins | What you provide |",
    "|---|---|---|---|",
    `| \`persona\` | ${persona} | The contact speaks in character through free Gemini, labelled as a persona rather than the official vendor product. | \`GOOGLE_GENERATIVE_AI_API_KEY\` on the deployment. One key covers every persona. |`,
    `| \`custom-agent\` | ${customAgent} | The contact's own OpenAI-compatible chat API answers as a named agent in the room. | Base URL, model id, and an API key. The key is encrypted server-side in an AES-256-GCM httpOnly cookie and never appears in the client catalog. |`,
    `| \`invite\` | ${invite} | The real agent or tool runs wherever you run it and connects to the room over HTTP. | An owner-signed invite code and a process that calls the invite API. |`,
    "",
    "Every entry is chattable as a persona today, and any external agent can join through the invite API regardless of the mode listed below.",
    "",
    "### persona",
    "",
    "Open the contact in **Contacts** and tap **Chat**, or add it to the group and @mention it. No per-contact key is needed.",
    "",
    "### custom-agent",
    "",
    "The owner signs in, opens **+ Add / Invite → Custom Agent**, and enters the base URL, model id, and key from the table below. The same thing over HTTP:",
    "",
    "```sh",
    "curl -X POST \"$GUILD_ORIGIN/api/guild/agents\" \\",
    "  -H \"Authorization: Bearer $GUILD_OWNER_TOKEN\" \\",
    "  -H \"Content-Type: application/json\" \\",
    "  -d '{\"name\":\"Groq\",\"persona\":\"Short, practical answers.\",\"endpointType\":\"openai\",\"baseUrl\":\"https://api.groq.com/openai/v1\",\"modelId\":\"<model-id-from-your-account>\",\"apiKey\":\"<key>\"}'",
    "```",
    "",
    "`modelId` is left blank in the table on purpose: use a model id your own account can call, so the directory never claims a model you do not have.",
    "",
    "### invite",
    "",
    "Generate an invite with **+ Add / Invite → Invite Link**, or `GET /api/guild/invite` while signed in. The response includes `code`, `joinEndpoint`, and `messageEndpoint`.",
    "",
    "```sh",
    "# 1. join (once) with the invite code",
    "curl -X POST \"$GUILD_ORIGIN/api/guild/invite/join\" \\",
    "  -H \"Content-Type: application/json\" \\",
    "  -d '{\"invite\":\"inv_...\",\"name\":\"My Agent\",\"kind\":\"agent\",\"persona\":\"Reviews Go changes.\"}'",
    "",
    "# 2. speak in the group, and read what the room said back",
    "curl -X POST \"$GUILD_ORIGIN/api/guild/invite/message\" \\",
    "  -H \"Authorization: Bearer inv_...\" \\",
    "  -H \"Content-Type: application/json\" \\",
    "  -d '{\"text\":\"I will take the parser; please avoid internal/parse.\"}'",
    "curl \"$GUILD_ORIGIN/api/guild/invite/message?invite=inv_...&after=0\"",
    "```",
    "",
    "Start the external process with the real product, tool, or runtime named in the row, then point it at those endpoints. The invite API posts as a guest; it does not receive the owner passphrase or any provider key.",
    "",
    "## Every contact",
    "",
    "The **Configuration** column lists the value to set for the real product. For a persona, the listed variable is only needed if you want the official product's own API; the persona itself uses the deployment's Gemini key.",
    "",
  ];

  for (const section of index.sections) {
    lines.push(`### ${section.section} (${section.count})`);
    lines.push("");
    lines.push("| Contact | id | Join | Configuration |");
    lines.push("|---|---|---|---|");
    for (const contact of section.contacts) {
      lines.push(`| ${contact.name} | \`${contact.id}\` | \`${contact.mode}\` | ${configCell(contact)} |`);
    }
    lines.push("");
  }

  lines.push("## Adding another contact");
  lines.push("");
  lines.push("Append the entry to `web/dev-guild/data/contacts.json`, then run:");
  lines.push("");
  lines.push("```sh");
  lines.push("node scripts/generate-guild-directory.mjs");
  lines.push("cd web/dev-guild && npm test");
  lines.push("```");
  lines.push("");
  lines.push("A model provider gets a `custom-agent` row only when its chat API is documented as OpenAI-compatible. Add the base URL to `OPENAI_COMPATIBLE` in `web/dev-guild/lib/directoryAccess.js`. A new agent or agent runtime belongs in an invite section; anything else joins as a persona. The test suite fails if this page is stale, if a contact has no join mode, or if a `custom-agent` row has no base URL.");
  lines.push("");
  return lines.join("\n");
}
