// Live check for the free Gemini API. Skips when the key is absent outside
// GitHub Actions. Prints whether the reply had text. Never prints the key.

import { generateText } from "ai";
import { google } from "@ai-sdk/google";

const key = process.env.GOOGLE_GENERATIVE_AI_API_KEY || "";
const inActions = process.env.GITHUB_ACTIONS === "true";

if (!key) {
  if (inActions) {
    console.log("non-empty: no");
    console.log("GOOGLE_GENERATIVE_AI_API_KEY is unset in this job");
    process.exit(1);
  }
  console.log("GOOGLE_GENERATIVE_AI_API_KEY unset; smoke skipped");
  process.exit(0);
}

function safe(err) {
  let message = String((err && err.message) || err || "request failed");
  if (key) message = message.split(key).join("[redacted]");
  return message.slice(0, 180);
}

try {
  const result = await generateText({
    model: google("gemini-3.5-flash-lite"),
    prompt: "Reply with one short sentence.",
    maxRetries: 0,
  });
  const text = String(result.text || "").trim();
  if (!text) {
    console.log("non-empty: no");
    process.exit(1);
  }
  console.log("non-empty: yes");
  console.log(`length: ${text.length}`);
} catch (err) {
  console.log("non-empty: no");
  console.log(safe(err));
  process.exit(1);
}
