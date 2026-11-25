// Server-only model clients. Keys are read from the environment by each SDK.
// This module is not imported by the unit tests.

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
  const error = new Error("unknown provider");
  error.status = 500;
  throw error;
}
