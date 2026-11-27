import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { JOIN_MODES, directoryIndex, joinFor, renderDirectoryMarkdown } from "./directoryAccess.js";
import {
  DEMO_STARTERS,
  REACTIONS,
  isReaction,
  roomVibe,
  starterPrompts,
  summarizeReactions,
  viewerIdFromAuth,
} from "./social.js";
import { addReaction, appendLine, getMessages, invitedMessages, resetStoreForTest } from "./store.js";

const DOC_PATH = fileURLToPath(new URL("../../../docs/GUILD-DIRECTORY.md", import.meta.url));

test("every one of the 451 directory contacts has a valid join path", () => {
  const index = directoryIndex();
  assert.equal(index.count, 451);
  assert.equal(index.sectionCount, 17);
  assert.equal(index.contacts.length, 451);
  assert.deepEqual(Object.keys(index.modeCounts).sort(), [...JOIN_MODES].sort());
  assert.equal(Object.values(index.modeCounts).reduce((sum, n) => sum + n, 0), 451);
  for (const contact of index.contacts) {
    assert.ok(JOIN_MODES.includes(contact.mode), `${contact.id} has no join mode`);
    assert.ok(contact.envKey, `${contact.id} has no environment key`);
    assert.ok(contact.docs.startsWith("docs/GUILD-DIRECTORY.md#"), `${contact.id} has no doc anchor`);
    if (contact.mode === "custom-agent") {
      assert.match(contact.baseUrl, /^https?:\/\//, `${contact.id} needs a base URL`);
    }
  }
  const coding = index.contacts.filter((c) => c.section === "AI coding agents");
  assert.equal(coding.length, 75);
  assert.ok(coding.every((c) => c.mode === "invite"));
  assert.ok(index.contacts.filter((c) => c.mode === "custom-agent").length >= 20);
});

test("joinFor never invents a base URL for a per-project provider", () => {
  const azure = joinFor({ id: "azure-openai", name: "Azure OpenAI", section: "AI APIs & model providers", requiredEnvKey: "AZURE_OPENAI_API_KEY" });
  assert.equal(azure.mode, "persona");
  assert.equal(azure.baseUrl, "");
  const groq = joinFor({ id: "groq", name: "Groq", section: "AI APIs & model providers", requiredEnvKey: "GROQ_API_KEY" });
  assert.equal(groq.mode, "custom-agent");
  assert.equal(groq.baseUrl, "https://api.groq.com/openai/v1");
});

test("docs/GUILD-DIRECTORY.md is generated from the current contacts", () => {
  assert.equal(readFileSync(DOC_PATH, "utf8"), renderDirectoryMarkdown());
});

test("reaction allowlist and summaries are deterministic", () => {
  assert.deepEqual(REACTIONS, ["👍", "❤️", "😂", "🎉", "👀", "🔥"]);
  assert.equal(isReaction("🎉"), true);
  assert.equal(isReaction("🍕"), false);
  assert.deepEqual(summarizeReactions({ "🔥": ["owner"], "👍": ["claude", "owner"] }, "owner"), [
    { emoji: "👍", count: 2, mine: true },
    { emoji: "🔥", count: 1, mine: true },
  ]);
});

test("a reaction toggles on and off and rides along with the message", async () => {
  resetStoreForTest();
  const msg = await appendLine({ threadId: "guild", from: "claude", text: "Small diff.", catalog: [] });
  const on = await addReaction({ threadId: "guild", messageId: msg.id, emoji: "🎉" });
  assert.equal(on.persisted, true);
  assert.deepEqual(on.reactions, [{ emoji: "🎉", count: 1, mine: true }]);
  assert.deepEqual((await getMessages("guild", 0))[0].reactions, [{ emoji: "🎉", count: 1, mine: true }]);

  const off = await addReaction({ threadId: "guild", messageId: msg.id, emoji: "🎉" });
  assert.deepEqual(off.reactions, []);
  assert.equal((await getMessages("guild", 0))[0].reactions, undefined);

  await assert.rejects(() => addReaction({ threadId: "guild", messageId: msg.id, emoji: "🍕" }), /unsupported/);
  await assert.rejects(() => addReaction({ threadId: "guild", messageId: "m_missing", emoji: "👍" }), /not found/);
});

test("an invited agent sees its own reaction, not the owner's", async () => {
  resetStoreForTest();
  const msg = await appendLine({ threadId: "guild", from: "claude", text: "Small diff.", catalog: [] });
  await addReaction({ threadId: "guild", messageId: msg.id, emoji: "🎉", from: "owner" });
  await addReaction({ threadId: "guild", messageId: msg.id, emoji: "👍", from: "review-bot" });

  const forAgent = await invitedMessages({
    auth: { role: "invite", participant: { id: "review-bot", name: "Review Bot" } },
  });
  assert.deepEqual(forAgent[0].reactions, [
    { emoji: "👍", count: 1, mine: true },
    { emoji: "🎉", count: 1, mine: false },
  ]);

  const forOwner = await invitedMessages({ auth: { role: "owner", participant: { id: "owner" } } });
  assert.deepEqual(forOwner[0].reactions, [
    { emoji: "👍", count: 1, mine: false },
    { emoji: "🎉", count: 1, mine: true },
  ]);

  assert.equal(viewerIdFromAuth({ role: "invite-token", participant: { id: "guest" } }), "guest");
  assert.equal(viewerIdFromAuth({ participant: { id: "review-bot" } }), "review-bot");
  assert.equal(viewerIdFromAuth(null), "guest");
});

test("demo reactions never persist", async () => {
  const result = await addReaction({ threadId: "guild", messageId: "m_1_abc", emoji: "👍", env: { GUILD_DEMO: "1" } });
  assert.equal(result.persisted, false);
  assert.deepEqual(result.reactions, []);
});

test("room vibe counts the people who can actually answer", () => {
  assert.equal(roomVibe({ members: ["owner"], onlineIds: [] }).label, "Empty room");
  assert.equal(
    roomVibe({ members: ["owner", "claude", "grok"], onlineIds: ["claude"] }).label,
    "2 here · 1 online",
  );
  assert.equal(roomVibe({ members: ["owner", "claude"], onlineIds: [], demo: true }).label, "1 demo agents · auto chat");
});

test("starter prompts mention online members and never invent one", () => {
  const characters = [
    { id: "claude", online: true },
    { id: "grok", online: true },
    { id: "offline", online: false },
  ];
  const prompts = starterPrompts({ members: ["owner", "claude", "grok", "offline"], characters });
  assert.equal(prompts.length, 3);
  assert.ok(prompts[0].includes("@claude"));
  assert.ok(prompts.some((p) => p.includes("@grok")));
  assert.equal(prompts.some((p) => p.includes("@offline")), false);
  assert.deepEqual(starterPrompts({ members: ["owner"], characters }), []);
  assert.deepEqual(starterPrompts({ demo: true }), DEMO_STARTERS);
});
