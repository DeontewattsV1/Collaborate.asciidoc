import { Render } from "@renderinc/sdk";

const WORKSPACE_ID = "tea-dau3urnavr4c73fj6l2g";
const SANDBOX_GROUP_ID = "sbg-dc225m56e07s73envfrg";

const { sandboxes } = new Render({ ownerId: WORKSPACE_ID });

const sandbox = await sandboxes.create({
  sandboxGroupId: SANDBOX_GROUP_ID,
  wait: true, // resolve once it's running
});
for await (const event of await sandboxes.exec(sandbox.id, "echo 'Hello from Render Sandboxes'")) {
  if (event.type === "output") process.stdout.write(event.data);
}
await sandboxes.terminate(sandbox.id);
