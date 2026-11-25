import directoryEntries from "../data/contacts.json" with { type: "json" };

export function directoryCharacters(env) {
  const source = env || {};
  const hasFreeModel = Boolean(
    source.GOOGLE_GENERATIVE_AI_API_KEY || source.GROQ_API_KEY || source.OPENROUTER_API_KEY,
  );
  return directoryEntries.map((entry) => ({
    id: entry.id,
    name: entry.name,
    kind: "directory",
    section: entry.section,
    color: entry.color || "#5E5CE6",
    avatar: entry.avatar || "",
    animal: entry.animal || "fox",
    emoji: entry.emoji || "🦊",
    look: `Chibi ${entry.animal || "fox"} mascot representing ${entry.name}.`,
    voice: `In-character persona for ${entry.name} (${entry.section}). Practical, concise, never claims to be the official vendor service.`,
    quirk: `Frames advice around ${entry.name}'s strengths and free-tier boundaries.`,
    catchphrase: `${entry.name} persona reporting in.`,
    role: `${entry.section} · Free Gemini persona`,
    talks: "Collaborates with the guild on tooling, workflows, and integration choices.",
    expertise: entry.persona,
    persona: entry.persona,
    requiredEnvKey: entry.requiredEnvKey || "GOOGLE_GENERATIVE_AI_API_KEY",
    freeTier: Boolean(entry.freeTier),
    isPersona: true,
    online: hasFreeModel,
    statusLabel: hasFreeModel ? "active" : "needs key",
    note: hasFreeModel
      ? "Free Gemini persona (not the official product)"
      : "Needs GOOGLE_GENERATIVE_AI_API_KEY",
    needsKey: !hasFreeModel,
    envVar: hasFreeModel ? "" : (entry.requiredEnvKey || "GOOGLE_GENERATIVE_AI_API_KEY"),
    hasKey: false,
    enable: `Voiced as a Free Gemini persona (${entry.requiredEnvKey || "GOOGLE_GENERATIVE_AI_API_KEY"} optional for real external use).`,
  }));
}
