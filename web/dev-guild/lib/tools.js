import { jsonSchema, tool } from "ai";
import { runTool } from "./models.js";

const pathSchema = jsonSchema({
  type: "object",
  properties: { path: { type: "string", description: "Repo-relative file path" } },
  required: ["path"],
  additionalProperties: false,
});

const handoffSchema = jsonSchema({
  type: "object",
  properties: {
    task: { type: "string" },
    assignee: { type: "string", description: "Character id, without @" },
    status: { type: "string", enum: ["open", "handed-off", "done"] },
  },
  required: ["task", "assignee", "status"],
  additionalProperties: false,
});

const emptySchema = jsonSchema({
  type: "object",
  properties: {},
  additionalProperties: false,
});

// guildTools is the safe set the phone model may call. Each call is logged
// by the guild bridge. File bytes are not copied into the signed log.
export function guildTools({ backend, threadId, from, known }) {
  const ctx = { backend, threadId, from, known };
  return {
    read_repo_file: tool({
      description: "Read one file in the guild checkout. Parent paths, .env, keys, and the signed log are refused.",
      inputSchema: pathSchema,
      execute: async (args) => runTool({ ...ctx, name: "read_repo_file", args }),
    }),
    lookup_log: tool({
      description: "Read the latest signed lines in this thread.",
      inputSchema: emptySchema,
      execute: async () => runTool({ ...ctx, name: "lookup_log", args: {} }),
    }),
    handoff: tool({
      description: "Write a task card and hand it to another character.",
      inputSchema: handoffSchema,
      execute: async (args) => runTool({ ...ctx, name: "handoff", args }),
    }),
  };
}
