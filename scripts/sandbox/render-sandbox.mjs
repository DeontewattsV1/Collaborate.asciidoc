// Launch one Render sandbox, report it, and terminate it unless SANDBOX_KEEP=true.
// Env: RENDER_API_KEY (read by the SDK), SANDBOX_PLAN, SANDBOX_TIMEOUT_SECONDS,
// SANDBOX_IDLE_TIMEOUT_SECONDS, SANDBOX_GROUP_ID, SANDBOX_ALLOW_DOMAINS (comma list).
import { Render } from '@renderinc/sdk'

const env = process.env
const sandboxes = new Render().sandboxes

const options = {
  timeoutSeconds: Number(env.SANDBOX_TIMEOUT_SECONDS || 300),
  wait: true
}
if (env.SANDBOX_PLAN) options.plan = env.SANDBOX_PLAN
if (env.SANDBOX_IDLE_TIMEOUT_SECONDS) options.idleTimeoutSeconds = Number(env.SANDBOX_IDLE_TIMEOUT_SECONDS)
if (env.SANDBOX_GROUP_ID) options.sandboxGroupId = env.SANDBOX_GROUP_ID
if (env.SANDBOX_ALLOW_DOMAINS) {
  options.networkPolicy = {
    type: 'allow-list',
    rules: env.SANDBOX_ALLOW_DOMAINS.split(',').map((d) => ({ domain: d.trim() })).filter((r) => r.domain)
  }
}

const sandbox = await sandboxes.create(options)
console.log(`sandbox ${sandbox.id} status=${sandbox.status}`)

const running = await sandboxes.list({ status: ['running'] })
console.log(`running sandboxes: ${running.length}`)

if (env.SANDBOX_KEEP === 'true') {
  console.log('SANDBOX_KEEP=true, leaving it running.')
} else {
  await sandboxes.terminate(sandbox.id)
  console.log(`terminated ${sandbox.id}`)
}
