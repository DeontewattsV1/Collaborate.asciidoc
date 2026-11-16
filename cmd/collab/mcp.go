package main

// Minimal JSON-RPC 2.0 MCP stdio adapter. Any model client supporting MCP
// can call these broker tools without exchanging Markdown handoff files.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type rpcInput struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type rpcOutput struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func objSchema(required []string, props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}
func field(t string) map[string]any { return map[string]any{"type": t} }
func mcpTools() []map[string]any {
	return []map[string]any{
		{"name": "collab_register", "description": "Register an agent under an unverified model label", "inputSchema": objSchema([]string{"agent", "model"}, map[string]any{"agent": field("string"), "model": field("string")})},
		{"name": "collab_say", "description": "Publish a live message to an agent or channel", "inputSchema": objSchema([]string{"agent", "to", "body"}, map[string]any{"agent": field("string"), "to": field("string"), "body": field("string")})},
		{"name": "collab_claim", "description": "Obtain exclusive advisory lease for a repository path prefix", "inputSchema": objSchema([]string{"agent", "path"}, map[string]any{"agent": field("string"), "path": field("string"), "ttl_seconds": field("integer")})},
		{"name": "collab_renew", "description": "Extend a lease before expiry", "inputSchema": objSchema([]string{"agent", "lease_id"}, map[string]any{"agent": field("string"), "lease_id": field("string"), "ttl_seconds": field("integer")})},
		{"name": "collab_release", "description": "Release an owned lease", "inputSchema": objSchema([]string{"agent", "lease_id"}, map[string]any{"agent": field("string"), "lease_id": field("string")})},
		{"name": "collab_receipt", "description": "Record a broker-signed Git SHA claim (not an independent commit attestation)", "inputSchema": objSchema([]string{"agent", "commit", "paths", "summary"}, map[string]any{"agent": field("string"), "commit": field("string"), "paths": map[string]any{"type": "array", "items": field("string")}, "summary": field("string")})},
		{"name": "collab_events", "description": "Read broker ledger events after a sequence number; use frequently for inter-agent coordination", "inputSchema": objSchema([]string{}, map[string]any{"after": field("integer")})},
	}
}
func mcpHandler(in rpcInput) (any, *rpcError) {
	switch in.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(in.Params, &p)
		// Negotiate a protocol version this small adapter actually implements.
		v := "2024-11-05"
		if p.ProtocolVersion == "2024-11-05" {
			v = p.ProtocolVersion
		}
		return map[string]any{"protocolVersion": v, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "collab", "version": "0.1.0"}}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(in.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid tool-call parameters"}
		}
		if len(p.Arguments) == 0 {
			p.Arguments = []byte("{}")
		}
		routes := map[string]string{
			"collab_register": "/v1/register", "collab_say": "/v1/message", "collab_claim": "/v1/lease", "collab_renew": "/v1/renew", "collab_release": "/v1/release", "collab_receipt": "/v1/receipt",
		}
		var b []byte
		var err error
		if p.Name == "collab_events" {
			var opts struct {
				After int64 `json:"after"`
			}
			if e := json.Unmarshal(p.Arguments, &opts); e != nil || opts.After < 0 {
				return nil, &rpcError{-32602, "invalid cursor"}
			}
			b, err = request("GET", "/v1/events?after="+strconv.FormatInt(opts.After, 10), nil)
		} else if dest, ok := routes[p.Name]; ok {
			b, err = request("POST", dest, p.Arguments)
		} else {
			return nil, &rpcError{-32602, "unknown collaboration tool"}
		}
		if err != nil {
			return map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		return map[string]any{"content": []map[string]string{{"type": "text", "text": string(b)}}}, nil
	default:
		return nil, &rpcError{-32601, "method not found"}
	}
}
func serveMCP(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 8192), 1<<20)
	encoder := json.NewEncoder(w)
	for scanner.Scan() {
		line := scanner.Bytes()
		var in rpcInput
		if err := json.Unmarshal(line, &in); err != nil {
			if err := encoder.Encode(rpcOutput{JSONRPC: "2.0", Error: &rpcError{-32700, "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if in.JSONRPC != "2.0" {
			if err := encoder.Encode(rpcOutput{JSONRPC: "2.0", ID: in.ID, Error: &rpcError{-32600, "invalid request"}}); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(in.Method, "notifications/") {
			continue
		}
		result, rpcErr := mcpHandler(in)
		if err := encoder.Encode(rpcOutput{JSONRPC: "2.0", ID: in.ID, Result: result, Error: rpcErr}); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("MCP input: %w", err)
	}
	return nil
}
