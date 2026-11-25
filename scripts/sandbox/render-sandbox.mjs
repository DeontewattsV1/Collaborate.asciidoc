// Launch a short-lived Render sandbox, upload the compiled `collaborate` binary
// and live two-agent demo script, stream real broker/agent output, and
// terminate the sandbox in `finally` unless SANDBOX_KEEP=true (in which case
// Render still auto-terminates it when SANDBOX_TIMEOUT_SECONDS expires).
//
// Required env:
//   RENDER_API_KEY               Render API token (never logged)
// Optional env:
//   RENDER_WORKSPACE_ID          Workspace ID (default: tea-dc3ge117lnhs73fnjpig)
//   SANDBOX_GROUP_ID             Sandbox group ID (default: sbg-dc3h680u01pc73eld1ng)
//   SANDBOX_PLAN                 Compute plan, e.g. 1c-2g, 2c-4g
//   SANDBOX_TIMEOUT_SECONDS      Lifetime in seconds >=60 (default: 300)
//   SANDBOX_IDLE_TIMEOUT_SECONDS Idle suspension seconds (>=300 or 0)
//   SANDBOX_ALLOW_DOMAINS        Comma-separated HTTPS allow-list domains
//   SANDBOX_KEEP                 "true" leaves sandbox running until timeoutSeconds
//   COLLAB_BINARY                Path to prebuilt linux/amd64 collaborate binary
//   RENDER_SDK_PATH              Optional path/specifier override for @renderinc/sdk
//   OPENAI_API_KEY / COLLAB_MODEL_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY / GOOGLE_API_KEY
//                                Optional LLM credentials forwarded into sandbox env
import { execFileSync } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const DEFAULT_WORKSPACE_ID = 'tea-dc3ge117lnhs73fnjpig'
const DEFAULT_GROUP_ID = 'sbg-dc3h680u01pc73eld1ng'
const FORWARDED_ENV_KEYS = [
  'COLLAB_MODEL_API_KEY',
  'COLLAB_MODEL_BASE_URL',
  'COLLAB_MODEL',
  'OPENAI_API_KEY',
  'OPENAI_BASE_URL',
  'OPENAI_MODEL',
  'ANTHROPIC_API_KEY',
  'ANTHROPIC_BASE_URL',
  'ANTHROPIC_MODEL',
  'GEMINI_API_KEY',
  'GOOGLE_API_KEY',
  'GEMINI_BASE_URL',
  'GEMINI_MODEL',
]

const env = process.env
const workspaceId = env.RENDER_WORKSPACE_ID || DEFAULT_WORKSPACE_ID
const groupId = env.SANDBOX_GROUP_ID || DEFAULT_GROUP_ID
const timeoutSeconds = Number(env.SANDBOX_TIMEOUT_SECONDS || 300)
if (!Number.isInteger(timeoutSeconds) || timeoutSeconds < 60) {
  throw new Error('SANDBOX_TIMEOUT_SECONDS must be an integer >= 60 so sandboxes always auto-terminate')
}

const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(here, '..', '..')
const demoScript = path.join(here, 'render-demo.sh')

let buildDir = null
let collabBinary = env.COLLAB_BINARY || ''
if (!collabBinary) {
  buildDir = mkdtempSync(path.join(tmpdir(), 'collab-render-bin-'))
  collabBinary = path.join(buildDir, 'collaborate')
  execFileSync('go', ['build', '-trimpath', '-buildvcs=false', '-o', collabBinary, './cmd/collaborate'], {
    cwd: repoRoot,
    env: { ...env, CGO_ENABLED: '0', GOOS: 'linux', GOARCH: 'amd64' },
    stdio: 'inherit',
  })
}

const sdkSpecifier = env.RENDER_SDK_PATH || '@renderinc/sdk'
const { Render } = await import(sdkSpecifier)
const sandboxes = new Render({ ownerId: workspaceId }).sandboxes

const sandboxEnv = {
  COLLAB_BIN: '/tmp/collaborate',
  KEEP_BROKER: env.SANDBOX_KEEP === 'true' ? 'true' : 'false',
}
for (const key of FORWARDED_ENV_KEYS) {
  if (env[key]) sandboxEnv[key] = env[key]
}

const options = {
  name: 'collaborate-live-demo',
  sandboxGroupId: groupId,
  timeoutSeconds,
  env: sandboxEnv,
  wait: true,
}
if (env.SANDBOX_PLAN) options.plan = env.SANDBOX_PLAN
if (env.SANDBOX_IDLE_TIMEOUT_SECONDS) options.idleTimeoutSeconds = Number(env.SANDBOX_IDLE_TIMEOUT_SECONDS)
if (env.SANDBOX_ALLOW_DOMAINS) {
  options.networkPolicy = {
    type: 'allow-list',
    rules: env.SANDBOX_ALLOW_DOMAINS.split(',').map((d) => ({ domain: d.trim() })).filter((r) => r.domain),
  }
}

let sandboxId = null
try {
  let sandbox
  try {
    sandbox = await sandboxes.create(options)
    sandboxId = sandbox.id
  } catch (err) {
    if (err && typeof err === 'object' && 'sandboxId' in err && err.sandboxId) {
      sandboxId = err.sandboxId
    }
    throw err
  }
  console.log(`sandbox ${sandbox.id} status=${sandbox.status} group=${groupId} timeoutSeconds=${sandbox.timeoutSeconds}`)

  await sandboxes.copyTo(sandbox.id, { localPath: collabBinary, remotePath: '/tmp/collaborate' })
  await sandboxes.copyTo(sandbox.id, { localPath: demoScript, remotePath: '/tmp/render-demo.sh' })

  const events = await sandboxes.exec(sandbox.id, 'chmod +x /tmp/collaborate /tmp/render-demo.sh && /tmp/render-demo.sh')
  let exitCode = 0
  for await (const event of events) {
    if (event.type === 'output') {
      const stream = event.stream === 'stderr' ? process.stderr : process.stdout
      stream.write(event.data)
    } else if (event.type === 'exit') {
      exitCode = event.exit_code
    }
  }
  console.log(`demo exit_code=${exitCode}`)
  if (exitCode !== 0) {
    process.exitCode = exitCode
  }
} finally {
  if (sandboxId) {
    if (env.SANDBOX_KEEP === 'true') {
      console.log(`SANDBOX_KEEP=true, leaving ${sandboxId} running (auto-terminates after ${timeoutSeconds}s).`)
    } else {
      await sandboxes.terminate(sandboxId)
      console.log(`terminated ${sandboxId}`)
    }
  }
  if (buildDir) {
    rmSync(buildDir, { recursive: true, force: true })
  }
}
