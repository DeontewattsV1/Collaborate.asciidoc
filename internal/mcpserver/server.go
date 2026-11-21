// Package mcpserver exposes a per-agent, least-capability MCP stdio bridge.
// It deliberately has no filesystem, shell, or arbitrary HTTP tools.
package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"collaborate/internal/agentregistry"
	"collaborate/internal/client"
	"collaborate/internal/store"
)

const protocolVersion = "2025-06-18"
const maxMessageBytes = 1 << 20

type Server struct {
	Broker   *client.Client
	Registry *agentregistry.Registry
	AgentID  string
}
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) Serve(in io.Reader, out io.Writer) error {
	if s == nil || s.Broker == nil {
		return errors.New("MCP broker client is required")
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 4096), maxMessageBytes)
	wr := bufio.NewWriter(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			if err = writeRPC(wr, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "Parse error"}}); err != nil {
				return err
			}
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" {
			if len(req.ID) == 0 {
				continue
			}
			if err := writeRPC(wr, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32600, Message: "Invalid Request"}}); err != nil {
				return err
			}
			continue
		}
		result, rpcErr := s.dispatch(req)
		if len(req.ID) == 0 {
			continue
		} // notifications receive no response
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
		if err := writeRPC(wr, resp); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read MCP stdio: %w", err)
	}
	return nil
}

func writeRPC(w *bufio.Writer, v rpcResponse) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = w.Write(b); err != nil {
		return err
	}
	if err = w.WriteByte('\n'); err != nil {
		return err
	}
	return w.Flush()
}
func (s *Server) dispatch(req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid initialize parameters"}
		}
		return map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "collaborate-agent", "version": "0.3.0"}}, nil
	case "ping":
		return map[string]any{}, nil
	case "notifications/initialized":
		return nil, nil
	case "tools/list":
		return map[string]any{"tools": s.authorizedToolDefinitions()}, nil
	case "tools/call":
		var p struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return nil, &rpcError{Code: -32602, Message: "invalid tool call parameters"}
		}
		text, err := s.call(p.Name, p.Arguments)
		if err != nil {
			return map[string]any{"content": []any{map[string]string{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		return map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "Method not found"}
	}
}

func toolDefinitions() []map[string]any {
	return []map[string]any{
		{"name": "collaborate_claim", "description": "Request non-overlapping path leases before editing. Paths are relative to the agent worktree.", "inputSchema": objectSchema(map[string]any{"intent": stringSchema("Why the paths are needed"), "paths": map[string]any{"type": "array", "items": map[string]string{"type": "string"}, "minItems": 1}}, []string{"intent", "paths"})},
		{"name": "collaborate_renew", "description": "Renew this agent's active path leases.", "inputSchema": objectSchema(map[string]any{}, nil)},
		{"name": "collaborate_release", "description": "Release this agent's leases for the given relative paths.", "inputSchema": objectSchema(map[string]any{"paths": map[string]any{"type": "array", "items": map[string]string{"type": "string"}, "minItems": 1}}, []string{"paths"})},
		{"name": "collaborate_say", "description": "Send a message to the shared collaboration ledger.", "inputSchema": objectSchema(map[string]any{"message": stringSchema("Message text")}, []string{"message"})},
		{"name": "collaborate_inbox", "description": "Read shared collaboration messages after a ledger sequence number.", "inputSchema": objectSchema(map[string]any{"after": map[string]any{"type": "integer", "minimum": 0}}, nil)},
		{"name": "collaborate_receipt", "description": "Ask the broker to derive a Git receipt for a full commit object ID; changed paths must have valid leases.", "inputSchema": objectSchema(map[string]any{"commit": stringSchema("Full Git commit object ID")}, []string{"commit"})},
	}
}
func objectSchema(properties map[string]any, required []string) map[string]any {
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func stringSchema(description string) map[string]string {
	return map[string]string{"type": "string", "description": description}
}

func (s *Server) authorizedToolDefinitions() []map[string]any {
	if s == nil || s.Registry == nil || s.AgentID == "" {
		return []map[string]any{}
	}
	all := toolDefinitions()
	allowed := make([]map[string]any, 0, len(all))
	for _, tool := range all {
		name, _ := tool["name"].(string)
		if s.Registry.Authorize(s.AgentID, "mcp:"+name) == nil {
			allowed = append(allowed, tool)
		}
	}
	return allowed
}

func decodeArg[T any](args map[string]json.RawMessage, key string, required bool) (T, error) {
	var zero T
	b, ok := args[key]
	if !ok {
		if required {
			return zero, fmt.Errorf("%s is required", key)
		}
		return zero, nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return zero, fmt.Errorf("invalid %s: %w", key, err)
	}
	return v, nil
}
func (s *Server) call(name string, args map[string]json.RawMessage) (string, error) {
	if s == nil || s.Registry == nil || s.AgentID == "" {
		return "", errors.New("MCP registry authorization is not configured")
	}
	if err := s.Registry.Authorize(s.AgentID, "mcp:"+name); err != nil {
		return "", err
	}
	if args == nil {
		args = map[string]json.RawMessage{}
	}
	allowed := map[string]map[string]bool{"collaborate_claim": {"intent": true, "paths": true}, "collaborate_renew": {}, "collaborate_release": {"paths": true}, "collaborate_say": {"message": true}, "collaborate_inbox": {"after": true}, "collaborate_receipt": {"commit": true}}[name]
	if allowed == nil {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	for key := range args {
		if !allowed[key] {
			return "", fmt.Errorf("unknown argument %q", key)
		}
	}
	var route string
	var payload any
	switch name {
	case "collaborate_claim":
		intent, e := decodeArg[string](args, "intent", true)
		if e != nil {
			return "", e
		}
		paths, e := decodeArg[[]string](args, "paths", true)
		if e != nil {
			return "", e
		}
		if strings.TrimSpace(intent) == "" || len(paths) == 0 {
			return "", errors.New("intent and at least one path are required")
		}
		route = "/v1/claim"
		payload = map[string]any{"intent": intent, "paths": paths}
	case "collaborate_renew":
		route = "/v1/renew"
		payload = map[string]any{}
	case "collaborate_release":
		paths, e := decodeArg[[]string](args, "paths", true)
		if e != nil {
			return "", e
		}
		if len(paths) == 0 {
			return "", errors.New("at least one path is required")
		}
		route = "/v1/release"
		payload = map[string]any{"paths": paths}
	case "collaborate_say":
		msg, e := decodeArg[string](args, "message", true)
		if e != nil {
			return "", e
		}
		if strings.TrimSpace(msg) == "" {
			return "", errors.New("message must not be empty")
		}
		route = "/v1/say"
		payload = map[string]string{"body": msg}
	case "collaborate_receipt":
		commit, e := decodeArg[string](args, "commit", true)
		if e != nil {
			return "", e
		}
		if strings.TrimSpace(commit) == "" {
			return "", errors.New("commit is required")
		}
		route = "/v1/receipt"
		payload = map[string]string{"commit": commit}
	case "collaborate_inbox":
		after := int64(0)
		if b, ok := args["after"]; ok {
			n, e := strconv.ParseInt(string(b), 10, 64)
			if e != nil || n < 0 {
				return "", errors.New("after must be a non-negative integer")
			}
			after = n
		}
		path := "/v1/events?session=" + url.QueryEscape(s.Broker.Session)
		b, e := s.Broker.Do("GET", path, nil)
		if e != nil {
			return "", e
		}
		var ev []store.Event
		if e = json.Unmarshal(b, &ev); e != nil {
			return "", e
		}
		out := make([]map[string]any, 0)
		for _, v := range ev {
			if v.Seq <= after || v.Type != "SAY" {
				continue
			}
			var p struct {
				Body string `json:"body"`
			}
			_ = json.Unmarshal(v.Payload, &p)
			out = append(out, map[string]any{"seq": v.Seq, "agent": v.Agent, "message": p.Body, "timestamp_ms": v.TS})
		}
		return jsonText(out)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
	b, err := s.Broker.Do("POST", route, payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
func jsonText(v any) (string, error) { b, err := json.Marshal(v); return string(b), err }
