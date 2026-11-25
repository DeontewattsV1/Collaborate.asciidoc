// Server-only model clients. Keys are read from the environment or server-side
// custom agent config. This module is not imported by unit tests.

import { google } from "@ai-sdk/google";
import { groq } from "@ai-sdk/groq";
import { createOpenAI } from "@ai-sdk/openai";

export function languageModel(attempt, env) {
  const source = env || process.env;
  if (attempt.provider === "google") return google(attempt.modelId);
  if (attempt.provider === "groq") return groq(attempt.modelId);
  if (attempt.provider === "openrouter") {
    const client = createOpenAI({
      name: "openrouter",
      baseURL: "https://openrouter.ai/api/v1",
      apiKey: source.OPENROUTER_API_KEY,
    });
    return client.chat(attempt.modelId);
  }
  if (attempt.provider === "huggingface") {
    const client = createOpenAI({
      name: "huggingface",
      baseURL: "https://router.huggingface.co/v1",
      apiKey: source.HF_TOKEN,
    });
    return client.chat(attempt.modelId);
  }
  if (attempt.provider === "ollama") {
    const host = String(source.OLLAMA_HOST || "").replace(/\/$/, "");
    const client = createOpenAI({
      name: "ollama",
      baseURL: `${host}/v1`,
      apiKey: "ollama",
    });
    return client.chat(attempt.modelId);
  }
  if (attempt.provider === "custom-openai") {
    const client = createOpenAI({
      name: attempt.name || "custom",
      baseURL: String(attempt.baseUrl || "").replace(/\/$/, ""),
      apiKey: attempt.apiKey,
    });
    return client.chat(attempt.modelId);
  }
  const error = new Error("unknown provider");
  error.status = 500;
  throw error;
}
