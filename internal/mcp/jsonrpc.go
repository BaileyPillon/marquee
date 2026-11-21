package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
)

const jsonrpcVersion = "2.0"

// JSON-RPC codes. The first group is plain JSON-RPC 2.0. The second group is
// reserved by revision 2026-07-28 of the MCP specification, which says that
// nothing else may be emitted from -32020 downwards, so Marquee's own rate-limit
// code (codeRateLimited) sits outside the reserved range.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603

	codeHeaderMismatch     = -32020
	codeUnsupportedVersion = -32022

	// codeRateLimited is Marquee's application code for "too many calls".
	codeRateLimited = 429
)

// rpcMessage is anything that can arrive in a POST body: a request, a
// notification or (which Marquee ignores) a response.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func (m rpcMessage) hasID() bool { return len(m.ID) > 0 }

func (m rpcMessage) idIsNull() bool { return bytes.Equal(bytes.TrimSpace(m.ID), []byte("null")) }

func (m rpcMessage) isResponse() bool {
	return m.Method == "" && (len(m.Result) > 0 || len(m.Error) > 0)
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// rpcResponse is a reply. An error reply for a message whose id could not be
// read carries "id": null.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func okResponse(id json.RawMessage, result any) *rpcResponse {
	if result == nil {
		result = struct{}{}
	}
	return &rpcResponse{JSONRPC: jsonrpcVersion, ID: id, Result: result}
}

func errResponse(id json.RawMessage, code int, msg string, data any) *rpcResponse {
	return &rpcResponse{JSONRPC: jsonrpcVersion, ID: id, Error: &rpcError{Code: code, Message: msg, Data: data}}
}

var errEmptyBatch = errors.New("empty batch")

// parseBody reads a POST body: one JSON-RPC message or a batch of them. A
// batch element that is not an object is kept as a message with no method, so
// the caller answers it with an Invalid Request error.
func parseBody(body []byte) (msgs []rpcMessage, batch bool, err error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, false, errors.New("empty body")
	}
	if body[0] == '[' {
		var raws []json.RawMessage
		if err := json.Unmarshal(body, &raws); err != nil {
			return nil, true, err
		}
		if len(raws) == 0 {
			return nil, true, errEmptyBatch
		}
		for _, raw := range raws {
			var m rpcMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				m = rpcMessage{} // answered as an invalid request
			}
			msgs = append(msgs, m)
		}
		return msgs, true, nil
	}
	var m rpcMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, false, err
	}
	return []rpcMessage{m}, false, nil
}
