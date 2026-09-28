package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// JSON-RPC 2.0 over stdio. MCP's stdio transport is one JSON message per
// line; LSP-style Content-Length headers are still accepted for clients that
// send them, and each reply goes out framed the way its request came in.

// wire is the output side: it remembers how the last request was framed.
type wire struct {
	io.Writer
	lines bool
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // omit for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
)

// readMessage returns the next message and whether it arrived as a bare
// line. A line starting with "{" is a whole message; anything else starts a
// header block. Blank lines between messages are skipped.
func readMessage(r *bufio.Reader) ([]byte, bool, error) {
	var first string
	for first == "" {
		line, err := r.ReadString('\n')
		first = strings.TrimSpace(line)
		if err != nil && (err != io.EOF || first == "") {
			return nil, false, err
		}
	}
	if strings.HasPrefix(first, "{") {
		return []byte(first), true, nil
	}
	headers := map[string]string{}
	line := first
	for {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		i := strings.IndexByte(line, ':')
		if i < 0 {
			return nil, false, fmt.Errorf("malformed header: %q", line)
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		headers[strings.ToLower(key)] = val
		next, err := r.ReadString('\n')
		if err != nil {
			return nil, false, err
		}
		line = next
	}
	cl, ok := headers["content-length"]
	if !ok {
		return nil, false, fmt.Errorf("missing Content-Length")
	}
	n, err := strconv.Atoi(cl)
	if err != nil || n < 0 {
		return nil, false, fmt.Errorf("invalid Content-Length: %q", cl)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, false, err
	}
	return buf, false, nil
}

func writeMessage(w io.Writer, payload []byte) error {
	if wr, ok := w.(*wire); ok && wr.lines {
		_, err := wr.Write(append(payload, '\n'))
		return err
	}
	var hdr bytes.Buffer
	fmt.Fprintf(&hdr, "Content-Length: %d\r\n\r\n", len(payload))
	if _, err := w.Write(hdr.Bytes()); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func writeResponse(w io.Writer, id json.RawMessage, result any) error {
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return writeMessage(w, b)
}

func writeRPCError(w io.Writer, id json.RawMessage, code int, message string) error {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return writeMessage(w, b)
}
