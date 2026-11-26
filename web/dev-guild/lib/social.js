// Small, browser-safe helpers that make the room feel like a room: reactions,
// a presence line, and conversation openers. No secrets, no I/O.

export const REACTIONS = ["👍", "❤️", "😂", "🎉", "👀", "🔥"];

export const DEMO_STARTERS = [
  "Hey @grok and @claude, why use Collaborate?",
  "@tay what can this group chat do?",
  "Debate monoliths vs microservices in 1 line each",
];

export function isReaction(value) {
  return REACTIONS.includes(String(value || ""));
}

// viewerIdFromAuth resolves the identity a room read is personalized for.
// An invited agent is not the owner, so its reaction state must be computed
// from its own participant id. Accepts either an auth object or a participant.
export function viewerIdFromAuth(auth, fallback = "guest") {
  const participant = auth && (auth.participant || auth);
  const id = participant && participant.id;
  return String(id || fallback).trim().slice(0, 40) || fallback;
}

// room keeps { emoji: [actorIds] } per message. The client receives counts and
// whether the viewer was one of the actors.
export function summarizeReactions(room, viewer = "owner") {
  return Object.entries(room || {})
    .filter(([, actors]) => Array.isArray(actors) && actors.length > 0)
    .map(([emoji, actors]) => ({ emoji, count: actors.length, mine: actors.includes(viewer) }))
    .sort((a, b) => REACTIONS.indexOf(a.emoji) - REACTIONS.indexOf(b.emoji));
}

export function roomVibe({ members, onlineIds, demo = false } = {}) {
  const people = (members || []).filter((id) => id && id !== "owner");
  const online = new Set(onlineIds || []);
  const around = people.filter((id) => online.has(id));
  if (demo) {
    return { label: `${people.length} demo agents · auto chat`, detail: "Jump in any time" };
  }
  if (!people.length) {
    return { label: "Empty room", detail: "Add an agent or invite someone" };
  }
  if (!around.length) {
    return { label: `${people.length} here · no voice online`, detail: "Add a free key or connect the VM" };
  }
  return {
    label: `${people.length} here · ${around.length} online`,
    detail: around.length === people.length ? "Everyone is around" : "Some are away",
  };
}

export function starterPrompts({ members, characters, demo = false, limit = 3 } = {}) {
  if (demo) return DEMO_STARTERS.slice(0, limit);
  const byId = new Map((characters || []).map((c) => [c.id, c]));
  const picks = (members || [])
    .filter((id) => id && id !== "owner")
    .map((id) => byId.get(id))
    .filter((c) => c && c.online)
    .slice(0, 3);
  if (!picks.length) return [];
  const prompts = [`Hey @${picks[0].id}, what are you working on?`];
  if (picks[1]) prompts.push(`@${picks[0].id} and @${picks[1].id}, what should we build next?`);
  if (picks[2]) prompts.push(`@${picks[2].id}, give me the short version.`);
  prompts.push("Anyone up for a quick review?");
  return Array.from(new Set(prompts)).slice(0, limit);
}
