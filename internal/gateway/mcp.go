package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file is the ONLY place in the repo that imports the MCP SDK. The
// streamable-HTTP handler runs stateless (no Mcp-Session-Id, JSON bodies
// instead of SSE) — right for a request-authenticated gateway on a small
// box. The /mcp route is wrapped in clientOnly like /v1/*, so every
// existing fg- client key works; tool handlers read the key back off the
// request-derived context for per-user attribution and metering.

const webToolsProvider = "web-tools"

// The tools' advertised schemas are written out by hand rather than inferred
// from a Go struct, because they deliberately widen every scalar to admit both
// its native encoding and the stringified one. See mcp_args.go for why: clients
// validate arguments against this schema before sending, so a scalar-typed
// property breaks every client whose tool arguments arrive stringified — which
// is the common case, and is not something the gateway can detect in advance.

// scalarSchema advertises one scalar argument, accepting either the native JSON
// type or a string carrying the same value. The description says which to send
// so a model that reads the schema still emits the native form.
func scalarSchema(typ, description string) map[string]any {
	return map[string]any{
		"type":        []string{typ, "string"},
		"description": description,
	}
}

var webSearchSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"properties": map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "the search query",
		},
		"maxResults": scalarSchema("integer",
			"maximum number of results to return (1-25, default 8). Send a number; a numeric string such as \"10\" is also accepted"),
	},
	"required": []string{"query"},
}

var webReadSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"properties": map[string]any{
		"url": map[string]any{
			"type":        "string",
			"description": "absolute http(s) URL of the page to read",
		},
		"render": scalarSchema("boolean",
			"render the page in a headless browser first (for JavaScript-heavy or bot-protected pages). Send true or false; \"true\"/\"false\" are also accepted"),
		"maxChars": scalarSchema("integer",
			"maximum characters of content to return. Send a number; a numeric string such as \"5000\" is also accepted"),
	},
	"required": []string{"url"},
}

// webMCPHandler lazily builds the SDK server + HTTP handler once. The tools are
// registered untyped (Server.AddTool) so this package owns argument decoding;
// see mcp_args.go for the coercions that make the tools work with the
// stringified scalars LLM clients actually send.
func (g *gateway) webMCPHandler() http.Handler {
	g.mcpOnce.Do(func() {
		srv := mcp.NewServer(&mcp.Implementation{
			Name:    "nano-llm-proxy-web-tools",
			Version: "1.0.0",
		}, nil)

		// Server.AddTool (not the generic mcp.AddTool) so the gateway keeps
		// ownership of argument decoding: the tools advertise widened scalar
		// types, and mcp_args.go coerces what actually arrives.
		srv.AddTool(&mcp.Tool{
			Name:        "web_search",
			Description: "Search the web and return a numbered list of results (title, URL, snippet). Uses the gateway's self-hosted SearXNG instance.",
			InputSchema: webSearchSchema,
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			args, err := decodeToolArgs(req.Params.Arguments)
			if err != nil {
				g.meterWebTool(ctx, "web_search", 400, 0, start)
				return toolError(err), nil
			}
			query, err := args.stringArg("query")
			if err != nil {
				g.meterWebTool(ctx, "web_search", 400, 0, start)
				return toolError(err), nil
			}
			maxResults, err := args.intArg("maxResults", 0)
			if err != nil {
				g.meterWebTool(ctx, "web_search", 400, 0, start)
				return toolError(err), nil
			}
			res, err := g.webtools.Search(ctx, query, maxResults)
			if err != nil {
				g.meterWebTool(ctx, "web_search", 502, 0, start)
				return toolError(err), nil
			}
			g.meterWebTool(ctx, "web_search", 200, len(res.Content), start)
			return textResult(res.Content), nil
		})

		srv.AddTool(&mcp.Tool{
			Name:        "web_read",
			Description: "Read a web page and return its main content as markdown. Plain pages use a fast native fetch; JavaScript-heavy or bot-protected pages are rendered with a headless browser (set render=true if content comes back empty or the site refuses plain fetches).",
			InputSchema: webReadSchema,
		}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			args, err := decodeToolArgs(req.Params.Arguments)
			if err != nil {
				g.meterWebTool(ctx, "web_read", 400, 0, start)
				return toolError(err), nil
			}
			url, err := args.stringArg("url")
			if err != nil {
				g.meterWebTool(ctx, "web_read", 400, 0, start)
				return toolError(err), nil
			}
			render, err := args.boolArg("render")
			if err != nil {
				g.meterWebTool(ctx, "web_read", 400, 0, start)
				return toolError(err), nil
			}
			maxChars, err := args.intArg("maxChars", 0)
			if err != nil {
				g.meterWebTool(ctx, "web_read", 400, 0, start)
				return toolError(err), nil
			}
			res, err := g.webtools.Read(ctx, url, render, maxChars)
			if err != nil {
				status := 502
				if isClientURLError(err) {
					status = 400
				}
				g.meterWebTool(ctx, "web_read", status, 0, start)
				return toolError(err), nil
			}
			g.meterWebTool(ctx, "web_read", 200, len(res.Content), start)
			content := res.Content
			if res.Method == "render" || (res.Method == "native" && res.Title != "") {
				content += fmt.Sprintf("\n\n_(via %s)_", res.Method)
			}
			return textResult(content), nil
		})

		g.mcpHandler = mcp.NewStreamableHTTPHandler(
			func(*http.Request) *mcp.Server { return srv },
			&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
		)
	})
	return g.mcpHandler
}

// handleMCP is the clientOnly-wrapped entry point.
func (g *gateway) handleMCP(w http.ResponseWriter, r *http.Request) {
	if !g.rs().WebTools.Enabled {
		writeErr(w, http.StatusNotFound, "Web tools are disabled")
		return
	}
	g.webMCPHandler().ServeHTTP(w, r)
}

// meterWebTool records one usage event + activity entry per tool call.
// Status semantics: 200 success, 400 client-fault (blocked/invalid URL),
// 502 upstream/tool failure.
func (g *gateway) meterWebTool(ctx context.Context, model string, status int, outChars int, start time.Time) {
	if g.store == nil {
		return
	}
	e := activityEntry{
		TS:         time.Now().Unix(),
		Provider:   webToolsProvider,
		Model:      model,
		Status:     status,
		DurationMs: time.Since(start).Milliseconds(),
	}
	if status >= 400 {
		e.FailReason = "tool error"
	}
	if ck, ok := clientKeyFromContext(ctx); ok {
		e.KeyAlias = ck.Alias
		e.User = ck.UserEmail
		g.usageBuf.add(store.UsageEvent{
			TS:           e.TS,
			UserID:       ck.UserID,
			ClientKeyID:  ck.ID,
			Provider:     webToolsProvider,
			Model:        model,
			OutputTokens: int64(outChars) / 4, // rough chars→tokens for usage views
			Status:       status,
			DurationMs:   e.DurationMs,
		})
	}
	g.usage.addActivity(e)
}

func textResult(md string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: md}}}
}

// toolError returns an isError result so the agent sees the failure and can
// adapt (retry, try render=true, give up) instead of crashing the call.
func toolError(err error) *mcp.CallToolResult {
	res := &mcp.CallToolResult{}
	res.SetError(err)
	return res
}

// isClientURLError distinguishes client-fault URLs (blocked/invalid) from
// upstream failures, for status metering only.
func isClientURLError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "blocked http") || strings.Contains(msg, "only http and https") ||
		strings.Contains(msg, "URL has no host") || strings.Contains(msg, "embedded credentials") ||
		strings.Contains(msg, "invalid URL")
}
