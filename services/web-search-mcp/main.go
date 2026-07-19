package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── Config ──────────────────────────────────────────────────────────────

var (
	port      = envInt("PORT", 8320)
	authToken = os.Getenv("AUTH_TOKEN")
)

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// ── JSON-RPC / MCP types ────────────────────────────────────────────────

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeResult struct {
	ProtocolVersion string            `json:"protocolVersion"`
	Capabilities    map[string]any    `json:"capabilities"`
	ServerInfo      map[string]string `json:"serverInfo"`
}

type toolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type callToolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type listToolsResult struct {
	Tools []toolDefinition `json:"tools"`
}

// ── Search ──────────────────────────────────────────────────────────────

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func searchWeb(query string, count int) ([]searchResult, error) {
	url := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", urlQueryEscape(query))
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DDG returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return parseDDGResults(string(body), count), nil
}

func parseDDGResults(html string, count int) []searchResult {
	var results []searchResult
	pos := 0

	for len(results) < count {
		block := strings.Index(html[pos:], `<h2 class="result__title">`)
		if block == -1 {
			break
		}
		block += pos

		// <a href="...">
		aOpen := strings.Index(html[block:], `<a `)
		if aOpen == -1 {
			break
		}
		aOpen += block

		hrefPos := strings.Index(html[aOpen:], `href="`)
		if hrefPos == -1 {
			break
		}
		hrefStart := aOpen + hrefPos + 6
		hrefEnd := strings.Index(html[hrefStart:], `"`)
		if hrefEnd == -1 {
			break
		}
		rawURL := html[hrefStart : hrefStart+hrefEnd]

		aTagClose := strings.Index(html[aOpen:], ">")
		if aTagClose == -1 {
			break
		}
		aTagEnd := strings.Index(html[aOpen:], "</a>")
		if aTagEnd == -1 {
			break
		}
		aTagEnd += aOpen
		titleText := stripTags(html[aOpen+aTagClose+1 : aTagEnd])

		// snippet: <a class="result__snippet"
		snipMarker := `class="result__snippet"`
		snipTag := strings.Index(html[aTagEnd:], snipMarker)
		if snipTag == -1 {
			pos = aTagEnd + 4
			continue
		}
		snipTag += aTagEnd
		snipOpen := strings.Index(html[snipTag:], ">")
		if snipOpen == -1 {
			break
		}
		snipEnd := strings.Index(html[snipTag+snipOpen:], "</a>")
		if snipEnd == -1 {
			break
		}
		snippet := stripTags(html[snipTag+snipOpen+1 : snipTag+snipOpen+snipEnd])

		results = append(results, searchResult{
			Title:   titleText,
			URL:     decodeURLQuery(rawURL),
			Snippet: snippet,
		})
		pos = snipTag + snipOpen + snipEnd + 4
	}
	return results
}

func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// urlQueryEscape encodes a query string for use in a URL query parameter.
func urlQueryEscape(s string) string {
	encoded := ""
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == ' ' {
			if c == ' ' {
				encoded += "+"
			} else {
				encoded += string(c)
			}
		} else {
			encoded += fmt.Sprintf("%%%02X", c)
		}
	}
	return encoded
}

// decodeURLQuery decodes percent-encoded URL.
func decodeURLQuery(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi := unhex(s[i+1])
			lo := unhex(s[i+2])
			if hi >= 0 && lo >= 0 {
				b.WriteByte(byte(hi<<4 | lo))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c - 'a' + 10)
	case c >= 'A' && c <= 'F':
		return int(c - 'A' + 10)
	}
	return -1
}

// ── MCP Session ─────────────────────────────────────────────────────────

type mcpSession struct {
	id       string
	msgs     chan []byte // SSE outbound message queue
	done     chan struct{}
	initDone bool
	mu       sync.Mutex
}

type mcpHandler func(json.RawMessage) (any, *jsonRPCError)

var (
	sessions   = make(map[string]*mcpSession)
	sessionsMu sync.Mutex
	handlers   = map[string]mcpHandler{
		"initialize":                handleInitialize,
		"notifications/initialized": handleInitialized,
		"tools/list":                handleListTools,
		"tools/call":                handleCallTool,
	}
)

func handleInitialize(params json.RawMessage) (any, *jsonRPCError) {
	return initializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{"tools": map[string]any{}},
		ServerInfo:      map[string]string{"name": "web-search-mcp", "version": "1.0.0"},
	}, nil
}

func handleInitialized(json.RawMessage) (any, *jsonRPCError) {
	return nil, nil // no response for notification
}

func handleListTools(json.RawMessage) (any, *jsonRPCError) {
	return listToolsResult{
		Tools: []toolDefinition{
			{
				Name:        "web_search",
				Description: "搜索网络，返回网页标题、URL 和摘要。使用 DuckDuckGo，无需 API Key。",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "搜索关键词"},
						"count": map[string]any{
							"type": "number", "description": "返回结果数量（默认 10，最大 20）",
						},
					},
					"required": []any{"query"},
				},
			},
		},
	}, nil
}

func handleCallTool(params json.RawMessage) (any, *jsonRPCError) {
	var args struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params"}
	}
	if args.Name != "web_search" {
		return nil, &jsonRPCError{Code: -32601, Message: "unknown tool: " + args.Name}
	}
	var toolArgs struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(args.Arguments, &toolArgs); err != nil {
		toolArgs.Query = ""
		toolArgs.Count = 10
	}
	query := strings.TrimSpace(toolArgs.Query)
	if query == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "query is required"}
	}
	count := toolArgs.Count
	if count <= 0 {
		count = 10
	}
	if count > 20 {
		count = 20
	}

	// 记录工具调用:只记查询词长度(不记内容),避免日志泄漏敏感查询。
	start := time.Now()
	slog.Info("tool call",
		slog.String("tool", args.Name),
		slog.Int("query_len", len(query)),
		slog.Int("count", count),
	)
	results, err := searchWeb(query, count)
	if err != nil {
		slog.Warn("tool call failed",
			slog.String("tool", args.Name),
			slog.Int("query_len", len(query)),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("error", err.Error()),
		)
		return callToolResult{
			Content: []contentItem{{Type: "text", Text: "Search failed: " + err.Error()}},
			IsError: true,
		}, nil
	}
	slog.Info("tool call done",
		slog.String("tool", args.Name),
		slog.Int("query_len", len(query)),
		slog.Int("results", len(results)),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	)
	data, _ := json.MarshalIndent(results, "", "  ")
	return callToolResult{
		Content: []contentItem{{Type: "text", Text: string(data)}},
	}, nil
}

func newSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func createSession() *mcpSession {
	s := &mcpSession{
		id:   newSessionID(),
		msgs: make(chan []byte, 64),
		done: make(chan struct{}),
	}
	sessionsMu.Lock()
	sessions[s.id] = s
	sessionsMu.Unlock()
	return s
}

func (s *mcpSession) close() {
	sessionsMu.Lock()
	delete(sessions, s.id)
	sessionsMu.Unlock()
	close(s.done)
}

func (s *mcpSession) sendEvent(event, data string) {
	msg := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, data))
	select {
	case s.msgs <- msg:
	case <-s.done:
	}
}

// ── HTTP Handlers ───────────────────────────────────────────────────────

// requestLogMiddleware 记录每个进入服务的 HTTP 请求的结构化访问日志。
//
// **为什么需要它**:在此之前本服务零请求日志,完全依赖 nginx 反代日志。
// MCP 的查询词(query)藏在 JSON-RPC POST body 里,nginx 默认不记录请求体,
// 因此"谁调了 web_search、查了什么"在 nginx 层完全不可见。这里在应用层
// 补上访问日志,记录 method/path/status/duration/client_ip;对 /message
// 的 MCP 调用额外记 JSON-RPC method 与工具名(见 handleMessage/handleCallTool)。
// 不记录查询词内容,只记其长度,避免日志泄漏敏感查询。
func requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		duration := time.Since(start).Milliseconds()
		level := slog.LevelInfo
		switch {
		case r.URL.Path == "/health":
			level = slog.LevelDebug
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status >= 400:
			level = slog.LevelWarn
		}
		// /sse 是长连接,WriteHeader 之前可能持续推送事件,
		// duration 仅反映连接建立耗时而非完整生命周期,此处仍记录以观察建连情况。
		slog.Default().LogAttrs(r.Context(), level, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.RequestURI()),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", duration),
			slog.String("client_ip", mcpClientIP(r)),
			slog.Int("bytes", rec.bytes),
		)
	})
}

// statusRecorder 包裹 http.ResponseWriter 以捕获状态码与已写字节数。
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// mcpClientIP 解析客户端真实 IP。
// web-search-mcp 经 nginx 反代(nginx.conf.tmpl 的 __WEB_SEARCH_PUBLIC_PATH__ location
// 注入 X-Real-IP / X-Forwarded-For),也可能被 8320 端口直连(无反代头)。按优先级取值。
func mcpClientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx >= 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func authMiddleware(next http.Handler) http.Handler {
	if authToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+authToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	session := createSession()
	defer session.close()

	// 第一个 event 告知客户端 POST 端点
	endpoint := fmt.Sprintf("/message?sessionId=%s", session.id)
	session.sendEvent("endpoint", endpoint)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-session.done:
			return
		case msg := <-session.msgs:
			_, err := w.Write(msg)
			if err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, `{"error":"sessionId required"}`, http.StatusBadRequest)
		return
	}

	sessionsMu.Lock()
	session, ok := sessions[sessionID]
	sessionsMu.Unlock()
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"read body failed"}`, http.StatusBadRequest)
		return
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		sendJSONRPCError(session, nil, -32700, "parse error")
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// notifications 不需要回复
	if req.ID == nil || len(req.ID) == 0 || string(req.ID) == "null" {
		handleNotification(session, req)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	handler, ok := handlers[req.Method]
	if !ok {
		sendJSONRPCError(session, req.ID, -32601, "method not found: "+req.Method)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// 检查 initialize 状态
	if req.Method != "initialize" && !session.initDone {
		sendJSONRPCError(session, req.ID, -32000, "server not initialized")
		w.WriteHeader(http.StatusAccepted)
		return
	}

	slog.Info("mcp call", slog.String("method", req.Method), slog.String("session", sessionID))
	result, rpcErr := handler(req.Params)
	if rpcErr != nil {
		slog.Warn("mcp call failed",
			slog.String("method", req.Method),
			slog.Int("code", rpcErr.Code),
			slog.String("message", rpcErr.Message),
		)
		sendJSONRPCError(session, req.ID, rpcErr.Code, rpcErr.Message)
	} else {
		sendJSONRPCResult(session, req.ID, result)
	}

	// 标记初始化完成
	if req.Method == "initialize" {
		session.mu.Lock()
		session.initDone = true
		session.mu.Unlock()
	}

	w.WriteHeader(http.StatusAccepted)
}

func handleNotification(session *mcpSession, req jsonRPCRequest) {
	if req.Method == "notifications/initialized" {
		session.mu.Lock()
		session.initDone = true
		session.mu.Unlock()
	}
}

func sendJSONRPCResult(session *mcpSession, id json.RawMessage, result any) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	session.sendEvent("message", string(data))
}

func sendJSONRPCError(session *mcpSession, id json.RawMessage, code int, msg string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: msg},
	}
	data, _ := json.Marshal(resp)
	session.sendEvent("message", string(data))
}

// ── Main ────────────────────────────────────────────────────────────────

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /sse", handleSSE)
	mux.HandleFunc("POST /message", handleMessage)

	// 请求日志在 authMiddleware 内层:既记录通过认证的真实请求,
	// 也记录 401 未授权(便于排查鉴权失败),与 nginx 反代日志互补。
	handler := authMiddleware(requestLogMiddleware(mux))

	log.Printf("web-search-mcp listening on http://0.0.0.0:%d/sse", port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
