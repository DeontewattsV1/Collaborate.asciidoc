# Third-party WASI provider plugins

`collaborate` can load third-party provider adapters compiled as WebAssembly System Interface (WASI) modules. It uses Wazero in-process; it does not use Go's native `plugin` package, so providers do not need to share the host's Go compiler version or run as native code.

A WASI module implements two stateless JSON phases. In `prepare`, it receives `{"phase":"prepare","model":"…","prompt":"…","max_tokens":1024}` and writes one JSON object to stdout: `{"path":"/provider/path","body":{...}}`. The host makes a single JSON `POST` to that path under the manifest's configured `base_url`. The response phase receives `{"phase":"response","response":{...provider JSON...}}` and writes `{"text":"completion text"}`. The host records non-empty text using the normal signed broker `say` capability. See [`examples/wasm/openai/main.go`](../examples/wasm/openai/main.go) for a working implementation.

## Build and run the example

From the repository root, build the sample for WASI Preview 1 using Go 1.21 or later:

```sh
mkdir -p /tmp/collaborate-provider
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build \
  -trimpath -o /tmp/collaborate-provider/openai-provider.wasm \
  ./examples/wasm/openai
```

Copy the manifest and module together to a private plugin directory. Edit `model` in `plugin.json` to a model supported by the chosen provider. The module file must be inside the manifest directory; symlink escapes and absolute module paths are rejected.

```sh
mkdir -p .collab/plugins/openai-example
cp examples/wasm/openai/plugin.json .collab/plugins/openai-example/plugin.json
cp /tmp/collaborate-provider/openai-provider.wasm .collab/plugins/openai-example/openai-provider.wasm
chmod 600 .collab/plugins/openai-example/plugin.json
chmod 700 .collab/plugins/openai-example
export COLLAB_DYNAMIC_API_KEY='your-provider-key'

collaborate adapter list --plugins .collab/plugins/openai-example
COLLAB_AGENT=agent-a collaborate adapter run \
  --plugin .collab/plugins/openai-example/plugin.json \
  --prompt 'Summarize the current task'
```

The example uses the OpenAI Chat Completions request shape. The same runtime can host another provider by supplying a different WASI module and manifest. A manifest names an API-key environment variable; the key itself belongs only in the host environment, never in the manifest or module. Manifests may include a `sha256` field to pin the exact module bytes; the loader rejects a mismatch. Keep manifests and credentials operator-controlled.

## Capability and resource boundary

Guest code receives JSON on stdin and can write JSON to stdout and diagnostics to stderr. It is instantiated without environment variables, command-line arguments, preopened directories, filesystem access, network imports, or broker handles. A fresh WASI instance is created for each phase. Wazero limits each guest's linear memory to 32 MiB, each invocation to 15 seconds, guest input/output to 8 MiB, and the provider request body to 2 MiB. The host bounds provider response bodies to 8 MiB, limits broker-published completion text to 1 MiB, and does not follow redirects.

Only the host performs network access. Dynamic manifests require an absolute HTTPS `base_url`; even loopback cleartext HTTP is rejected for third-party modules. The host sends a single JSON `POST` to the fixed origin from the reviewed manifest, adds the API key as the configured authentication header, and adds only the manifest's static headers. The module may choose a path on that same origin, but cannot choose a host, URL query, fragment, HTTP method, headers, or redirect destination. The adapter itself can publish only through the authenticated broker capability and currently uses it for `say`; guest code cannot read or write the repository or ledger directly.

This is defense-in-depth, not proof that third-party code is trustworthy. Review the module and manifest before loading them. A manifest can direct the host to a provider URL that receives the named key; use only trusted HTTPS endpoints. WASI modules may still consume their bounded CPU and memory allowance or return misleading content. Model output remains untrusted. Do not run native plugins from untrusted sources in the broker or model-host process.

The current JSON protocol is described here and distinguishes its two calls with the `phase` field. It supports one request/response completion per `Run`, JSON provider APIs, and a fixed endpoint per manifest. It does not yet support streaming, tools/function calls, arbitrary broker methods, or dynamic runtime unloading beyond closing the loaded adapter.
