package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	URL       string
	MaxTokens int
}

const (
	_default = "glm-4.7-flash"
)

var models = map[string]Config{
	"glm-4.7": {
		URL:       "https://api.z.ai/api/coding/paas/v4/chat/completions",
		MaxTokens: 8192,
	},
	"glm-4.7-flash": {
		URL:       "https://api.z.ai/api/paas/v4/chat/completions",
		MaxTokens: 8192,
	},
}

var messageLevelFields = []string{
	"tool_calls",
	"function_call",
	"reasoning_content",
	"metadata",
	"audio",
	"mcp_calls",
	"mcp_metadata",
}

type handler struct {
	key    string
	client *http.Client
}

var (
	listen  = flag.String("listen", "127.0.0.1:5000", "server listen address")
	model   = flag.String("model", _default, "model tag")
	timeout = flag.Int("timeout", 0, "timeout for server connections")
)

func main() {
	rand.Seed(time.Now().UnixNano())
	flag.Parse()

	key := os.Getenv("ZAI_API_KEY")
	if key == "" {
		log.Println("ZAI_API_KEY is empty the key from Authorization header will be used")
	}
	if _, ok := models[*model]; !ok {
		log.Fatalf("model tag must be one of %v", keys(models))
	}

	h := &handler{
		key: key,
		client: &http.Client{
			Timeout: time.Duration(*timeout) * time.Second,
		},
	}

	server := &http.Server{
		Addr:    *listen,
		Handler: h,
	}

	log.Println("start on:", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalln(err)
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodOptions:
		h.handleOptions(w)
	case http.MethodGet:
		h.handleGet(w, r)
	case http.MethodPost:
		h.handlePost(w, r)
	default:
		h.sendErrorJSON(w, http.StatusNotFound, "Not found")
	}
}

func (h *handler) handleOptions(w http.ResponseWriter) {
	h.addCORSHeaders(w)
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

func (h *handler) handleGet(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/models", "/models":
		data := make([]map[string]any, 0, len(models))
		for id := range models {
			data = append(data, map[string]any{
				"id":       id,
				"object":   "model",
				"created":  1700000000,
				"owned_by": "zhipuai",
			})
		}
		h.sendJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data":   data,
		})
	case "/health":
		h.sendJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"models": keys(models),
		})
	default:
		h.sendErrorJSON(w, http.StatusNotFound, "Not found")
	}
}

func (h *handler) handlePost(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/chat/completions", "/chat/completions":
		h.handleChat(w, r)
	default:
		h.sendErrorJSON(w, http.StatusNotFound, "Not found")
	}
}

func (h *handler) handleChat(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	payload, err := decodeJSONMap(r.Body)
	if err != nil {
		h.sendErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("Invalid body: %v", err))
		return
	}

	modelName := stringValue(payload["model"], _default)
	cfg, ok := models[modelName]
	if !ok {
		modelName = _default
		cfg = models[_default]
	}
	stream, _ := boolValue(payload["stream"])
	payload["model"] = rawJSON(modelName)
	payload["stream"] = rawJSON(stream)
	ensureMessages(payload)
	ensureTemperature(payload)
	payload["max_tokens"] = rawJSON(clampTokens(payload["max_tokens"], cfg.MaxTokens))

	data, err := json.Marshal(payload)
	if err != nil {
		h.sendErrorJSON(w, http.StatusInternalServerError, fmt.Sprintf("Encode error: %v", err))
		return
	}

	req, err := http.NewRequest(http.MethodPost, cfg.URL, bytes.NewReader(data))
	if err != nil {
		h.sendErrorJSON(w, http.StatusInternalServerError, fmt.Sprintf("Request error: %v", err))
		return
	}

	key := req.Header.Get("Authorization")
	if key == "" {
		key = "Bearer " + h.key
	}

	req.Header.Set("Authorization", key)
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := h.client.Do(req)
	if err != nil {
		h.sendErrorJSON(w, http.StatusBadGateway, fmt.Sprintf("Connection error: %v", err))
		return
	}

	if resp.StatusCode >= 400 {
		h.handleUpstreamError(w, resp, start)
		return
	}

	if stream {
		h.handleStream(w, resp, modelName)
		return
	}

	defer resp.Body.Close()
	h.handleNormal(w, resp, modelName, time.Since(start))
}

func (h *handler) handleUpstreamError(w http.ResponseWriter, resp *http.Response, start time.Time) {
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	msg := strings.TrimSpace(string(bodyBytes))
	var parsed map[string]any
	if err := json.Unmarshal(bodyBytes, &parsed); err == nil {
		if errMap, ok := parsed["error"].(map[string]any); ok {
			if text, ok := errMap["message"].(string); ok && text != "" {
				msg = text
			}
		}
	}
	if msg == "" {
		msg = fmt.Sprintf("upstream error %d", resp.StatusCode)
	}
	log.Printf("upstream %d (%.1fs)", resp.StatusCode, time.Since(start).Seconds())
	h.sendErrorJSON(w, resp.StatusCode, msg)
}

func (h *handler) handleNormal(w http.ResponseWriter, resp *http.Response, model string, elapsed time.Duration) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.sendErrorJSON(w, http.StatusBadGateway, fmt.Sprintf("Read error: %v", err))
		return
	}

	normalized, tokens, err := normalizeResponse(body, model)
	if err != nil {
		h.sendErrorJSON(w, http.StatusBadGateway, fmt.Sprintf("Invalid response: %v", err))
		return
	}
	log.Printf("%s -> %s tok, %.1fs", model, tokens, elapsed.Seconds())
	h.writeJSONBytes(w, http.StatusOK, normalized)
}

func (h *handler) handleStream(w http.ResponseWriter, resp *http.Response, model string) {
	defer resp.Body.Close()
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.sendErrorJSON(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}

	h.addCORSHeaders(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	chatID := makeOpenAIID()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	doneSent := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[5:])
		if payload == "[DONE]" {
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			doneSent = true
			break
		}

		frame, err := normalizeStreamChunk([]byte(payload), model, chatID)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "data: %s\n\n", frame)
		flusher.Flush()
	}

	if err := scanner.Err(); err != nil {
		log.Println("stream error:", err)
	}
	if !doneSent {
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

func (h *handler) sendJSON(w http.ResponseWriter, status int, data any) {
	body, err := json.Marshal(data)
	if err != nil {
		h.sendErrorJSON(w, http.StatusInternalServerError, fmt.Sprintf("Marshal error: %v", err))
		return
	}
	h.writeJSONBytes(w, status, body)
}

func (h *handler) writeJSONBytes(w http.ResponseWriter, status int, body []byte) {
	h.addCORSHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Connection", "close")
	w.WriteHeader(status)
	w.Write(body)
}

func (h *handler) sendErrorJSON(w http.ResponseWriter, status int, message string) {
	payload := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "api_error",
			"code":    status,
		},
	}
	h.sendJSON(w, status, payload)
}

func (h *handler) addCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
}

func decodeJSONMap(r io.Reader) (map[string]json.RawMessage, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		payload = map[string]json.RawMessage{}
	}
	return payload, nil
}

func ensureMessages(m map[string]json.RawMessage) {
	if raw := m["messages"]; isNullJSON(raw) {
		m["messages"] = rawJSON([]any{})
	}
}

func ensureTemperature(m map[string]json.RawMessage) {
	if raw, ok := m["temperature"]; !ok || isNullJSON(raw) {
		m["temperature"] = rawJSON(0.7)
	}
}

func clampTokens(raw json.RawMessage, limit int) int {
	if limit <= 0 {
		return 0
	}
	base := min(4096, limit)
	if n, ok := intValue(raw); ok {
		if n < 1 {
			n = base
		}
		if n > limit {
			n = limit
		}
		return n
	}
	return base
}

func normalizeResponse(body []byte, model string) ([]byte, string, error) {
	resp, err := decodeJSONMap(bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	if len(resp) == 0 {
		resp = map[string]json.RawMessage{}
	}
	if _, ok := resp["id"]; !ok {
		resp["id"] = rawJSON(makeOpenAIID())
	}
	if _, ok := resp["object"]; !ok {
		resp["object"] = rawJSON("chat.completion")
	}
	if _, ok := resp["created"]; !ok {
		resp["created"] = rawJSON(time.Now().Unix())
	}
	resp["model"] = rawJSON(model)
	resp["choices"] = normalizeChoices(resp["choices"])
	tokens := rawToText(extractNested(resp, "usage", "total_tokens"))
	if tokens == "" {
		tokens = "?"
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		return nil, "", err
	}
	return encoded, tokens, nil
}

func normalizeStreamChunk(raw []byte, model, fallbackID string) ([]byte, error) {
	chunk, err := decodeJSONMap(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if _, ok := chunk["id"]; !ok {
		chunk["id"] = rawJSON(fallbackID)
	}
	if _, ok := chunk["object"]; !ok {
		chunk["object"] = rawJSON("chat.completion.chunk")
	}
	if _, ok := chunk["created"]; !ok {
		chunk["created"] = rawJSON(time.Now().Unix())
	}
	chunk["model"] = rawJSON(model)
	chunk["choices"] = normalizeStreamChoices(chunk["choices"])
	return json.Marshal(chunk)
}

func normalizeChoices(raw json.RawMessage) json.RawMessage {
	choices := decodeArray(raw)
	if len(choices) == 0 {
		return mustMarshal([]map[string]json.RawMessage{defaultChoice()})
	}
	for idx := range choices {
		if _, ok := choices[idx]["index"]; !ok {
			choices[idx]["index"] = rawJSON(idx)
		}
		msg := buildChoiceMessage(choices[idx])
		choices[idx]["message"] = mustMarshal(msg)
		delete(choices[idx], "delta")
	}
	return mustMarshal(choices)
}

func normalizeStreamChoices(raw json.RawMessage) json.RawMessage {
	choices := decodeArray(raw)
	if len(choices) == 0 {
		return mustMarshal(choices)
	}
	for idx := range choices {
		if _, ok := choices[idx]["index"]; !ok {
			choices[idx]["index"] = rawJSON(idx)
		}
		msg := buildDeltaMessage(choices[idx])
		if msg != nil {
			choices[idx]["delta"] = mustMarshal(msg)
		} else {
			delete(choices[idx], "delta")
		}
		delete(choices[idx], "message")
	}
	return mustMarshal(choices)
}

func buildChoiceMessage(choice map[string]json.RawMessage) map[string]json.RawMessage {
	if msg := decodeMap(choice["message"]); len(msg) != 0 {
		enforceMessageDefaults(msg)
		mergeMessageFields(choice, msg)
		return msg
	}
	msg := decodeMap(choice["delta"])
	if len(msg) == 0 {
		msg = map[string]json.RawMessage{}
	}
	enforceMessageDefaults(msg)
	mergeMessageFields(choice, msg)
	return msg
}

func buildDeltaMessage(choice map[string]json.RawMessage) map[string]json.RawMessage {
	msg := decodeMap(choice["delta"])
	if len(msg) == 0 {
		msg = decodeMap(choice["message"])
	}
	if len(msg) == 0 {
		return nil
	}
	enforceMessageDefaults(msg)
	mergeMessageFields(choice, msg)
	return msg
}

func enforceMessageDefaults(msg map[string]json.RawMessage) {
	if role := stringValue(msg["role"], ""); role == "" {
		msg["role"] = rawJSON("assistant")
	}
	if _, ok := msg["content"]; !ok {
		msg["content"] = rawJSON("")
	}
}

func mergeMessageFields(choice, msg map[string]json.RawMessage) {
	for _, field := range messageLevelFields {
		if val, ok := choice[field]; ok {
			if _, exists := msg[field]; !exists {
				msg[field] = val
			}
		}
	}
}

func decodeArray(raw json.RawMessage) []map[string]json.RawMessage {
	if isNullJSON(raw) {
		return nil
	}
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	return arr
}

func decodeMap(raw json.RawMessage) map[string]json.RawMessage {
	if isNullJSON(raw) {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func defaultChoice() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"index":         rawJSON(0),
		"finish_reason": rawJSON("stop"),
		"message": mustMarshal(map[string]json.RawMessage{
			"role":    rawJSON("assistant"),
			"content": rawJSON(""),
		}),
	}
}

func isNullJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func rawJSON(value any) json.RawMessage {
	b, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return b
}

func mustMarshal(value any) json.RawMessage {
	b, _ := json.Marshal(value)
	return b
}

func stringValue(raw json.RawMessage, fallback string) string {
	if isNullJSON(raw) {
		return fallback
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return fallback
		}
		return s
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return strconv.FormatInt(int64(f), 10)
	}
	return fallback
}

func boolValue(raw json.RawMessage) (bool, bool) {
	if isNullJSON(raw) {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(strings.ToLower(s))
		switch s {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	}
	return false, false
}

func intValue(raw json.RawMessage) (int, bool) {
	if isNullJSON(raw) {
		return 0, false
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int(f), true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			return v, true
		}
	}
	return 0, false
}

func rawToText(raw json.RawMessage) string {
	if isNullJSON(raw) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return s
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return strconv.Itoa(n)
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return strconv.Itoa(int(f))
	}
	return ""
}

func extractNested(root map[string]json.RawMessage, keys ...string) json.RawMessage {
	current := root
	for idx, key := range keys {
		raw, ok := current[key]
		if !ok {
			return nil
		}
		if isNullJSON(raw) {
			return nil
		}
		if idx == len(keys)-1 {
			return raw
		}
		var next map[string]json.RawMessage
		if err := json.Unmarshal(raw, &next); err != nil {
			return nil
		}
		current = next
	}
	return nil
}

func makeOpenAIID() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 29)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return "chatcmpl-" + string(b)
}

func keys(m map[string]Config) []string {
	list := make([]string, 0, len(m))
	for k := range m {
		list = append(list, k)
	}
	return list
}
