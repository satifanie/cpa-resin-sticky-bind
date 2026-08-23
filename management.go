package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/satifanie/cpa-resin-sticky-bind/internal/stickybind"
)

const (
	resourcePath        = "/status"
	resourceContentType = "text/html; charset=utf-8"
)

type managementRegistration struct {
	Routes    []managementRoute    `json:"routes,omitempty"`
	Resources []managementResource `json:"resources,omitempty"`
}

// Use exported field names (no custom json tags) so CPA host decoding matches pluginapi.ResourceRoute.
type managementResource struct {
	Path        string
	Menu        string
	Description string
}

type managementRoute struct {
	Method      string
	Path        string
	Menu        string
	Description string
}

type managementRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Query   url.Values
	Body    []byte
}

type managementResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

func buildManagementRegistration() managementRegistration {
	return managementRegistration{
		// Browser resource under /v0/resource/plugins/<pluginID>/status
		Resources: []managementResource{{
			Path:        resourcePath,
			Menu:        "Resin Sticky Bind",
			Description: "Status and manual sync for per-auth Resin sticky proxy binding.",
		}},
		// Legacy GET+Menu also maps into resource routes on some CPA builds.
		Routes: []managementRoute{{
			Method:      http.MethodGet,
			Path:        resourcePath,
			Menu:        "Resin Sticky Bind",
			Description: "Status and manual sync for per-auth Resin sticky proxy binding.",
		}},
	}
}

func handleManagement(raw []byte) ([]byte, error) {
	var req managementRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("decode management request: %w", err)
		}
	}
	// Host passes the full request path, e.g. /v0/resource/plugins/cpa-resin-sticky-bind/status
	if !isStatusResourcePath(req.Path) {
		return okEnvelope(htmlResponse(http.StatusNotFound, "<html><body>not found</body></html>"))
	}

	cfg, halted := getRuntime().State()
	action := ""
	if req.Query != nil {
		action = strings.ToLower(strings.TrimSpace(req.Query.Get("action")))
	}
	var syncNote string
	switch action {
	case "sync":
		wrote, skipped, unchanged, failed, err := runManualSync(cfg)
		if err != nil {
			syncNote = "sync error: " + err.Error()
		} else {
			syncNote = fmt.Sprintf("sync done wrote=%d skipped=%d unchanged=%d failed=%d", wrote, skipped, unchanged, failed)
		}
	case "reset":
		// 先置位 reset 闩锁再清理：清理会写 auth 文件，host 的 auth watcher
		// 随即下发 plugin.reconfigure，闩锁保证 enabled 不被拉回 true。
		cfg, halted = getRuntime().HaltForReset(), true
		cleared, skipped, failed, err := runManualReset(cfg)
		if err != nil {
			syncNote = "reset error: " + err.Error()
		} else {
			syncNote = fmt.Sprintf("reset done cleared=%d skipped=%d failed=%d; sync loop halted until Resume", cleared, skipped, failed)
		}
	case "resume":
		cfg, halted = getRuntime().Resume(), false
		syncNote = fmt.Sprintf("resume done; sync loop follows config (enabled=%v)", cfg.Enabled)
	}

	page := renderStatusPage(cfg, halted, syncNote)
	return okEnvelope(htmlResponse(http.StatusOK, page))
}

func isStatusResourcePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" || path == resourcePath {
		return true
	}
	path = strings.TrimRight(path, "/")
	return strings.HasSuffix(path, resourcePath) || strings.HasSuffix(path, "/status")
}

func runManualSync(cfg stickybind.Config) (wrote, skipped, unchanged, failed int, err error) {
	return newBinder(cfg).SyncOnce()
}

func runManualReset(cfg stickybind.Config) (cleared, skipped, failed int, err error) {
	return newBinder(cfg).ResetOnce()
}

func newBinder(cfg stickybind.Config) *stickybind.Binder {
	return &stickybind.Binder{
		Cfg:    cfg,
		Host:   hostAdapter{},
		Getenv: os.Getenv,
	}
}

func htmlResponse(status int, body string) managementResponse {
	headers := make(http.Header)
	headers.Set("Content-Type", resourceContentType)
	return managementResponse{
		StatusCode: status,
		Headers:    headers,
		Body:       []byte(body),
	}
}

func renderStatusPage(cfg stickybind.Config, halted bool, syncNote string) string {
	cfg = cfg.Normalize()
	tokenStatus := "missing"
	if _, err := stickybind.ParseResinProxyURL(cfg.ResinProxyURL, cfg.ProxyTokenEnv, os.Getenv); err == nil {
		tokenStatus = "ok"
	} else {
		tokenStatus = "error: " + err.Error()
	}
	// never echo raw token/url password
	safeURL := stickybind.RedactProxyURL(cfg.ResinProxyURL)
	if safeURL == "" {
		safeURL = cfg.ResinProxyURL
	}
	// if URL has only username token, redact whole userinfo
	if u, err := url.Parse(cfg.ResinProxyURL); err == nil && u.User != nil {
		if _, hasPass := u.User.Password(); !hasPass && u.User.Username() != "" {
			safeURL = u.Scheme + "://***@" + u.Host
		}
	}

	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>Resin Sticky Bind</title>")
	b.WriteString("<style>body{font-family:system-ui,sans-serif;margin:24px;max-width:900px}code,pre{background:#f4f4f5;padding:2px 6px;border-radius:4px}table{border-collapse:collapse;width:100%}td,th{border:1px solid #ddd;padding:8px;text-align:left}.ok{color:#067d3e}.bad{color:#b00020}.note{margin:12px 0;padding:10px;background:#eef6ff;border-radius:6px}a.danger{color:#b00020}</style>")
	b.WriteString("</head><body>")
	b.WriteString("<h1>Resin Sticky Bind</h1>")
	b.WriteString("<p>Binds empty credential <code>proxy_url</code> values to stable Resin sticky accounts.</p>")
	if syncNote != "" {
		b.WriteString("<div class=\"note\">")
		b.WriteString(html.EscapeString(syncNote))
		b.WriteString("</div>")
	}
	b.WriteString("<p><a href=\"?action=sync\">Run sync now</a></p>")
	b.WriteString("<p><a class=\"danger\" href=\"?action=reset\" onclick=\"return confirm('Clear plugin-managed proxy_url from all credentials and halt the sync loop?')\">Reset All</a></p>")
	if halted {
		b.WriteString("<p><a href=\"?action=resume\">Resume sync loop</a></p>")
	}
	b.WriteString("<h2>Status</h2><table>")
	row(&b, "plugin", pluginID+" v"+pluginVer)
	row(&b, "enabled", fmt.Sprintf("%v", cfg.Enabled))
	row(&b, "reset_halted", fmt.Sprintf("%v", halted))
	row(&b, "resin_proxy_url", safeURL)
	row(&b, "proxy_token_env", cfg.ProxyTokenEnv)
	row(&b, "token_resolve", tokenStatus)
	row(&b, "default_platform", cfg.DefaultPlatform)
	row(&b, "account_strategy", cfg.AccountStrategy)
	row(&b, "sync_interval_seconds", fmt.Sprintf("%d", cfg.SyncIntervalSeconds))
	row(&b, "only_if_empty", fmt.Sprintf("%v", cfg.OnlyIfEmpty))
	row(&b, "overwrite_existing", fmt.Sprintf("%v", cfg.OverwriteExisting))
	row(&b, "time", time.Now().Format(time.RFC3339))
	b.WriteString("</table>")
	b.WriteString("<h2>Notes</h2><ul>")
	b.WriteString("<li>No Management API URL is required; host.auth callbacks run in-process.</li>")
	b.WriteString("<li>Token may come from env, URL password, or URL username-only form.</li>")
	b.WriteString("<li>Existing non-empty proxy_url is skipped when only_if_empty=true.</li>")
	b.WriteString("<li>Reset All matches on scheme + Platform.Account + host:port, ignoring the token, so rotated tokens are still cleared; manual proxy_url values are kept.</li>")
	b.WriteString("<li>Reset All covers disabled credentials and providers excluded by the current filters, so leftover bindings are cleared too.</li>")
	b.WriteString("<li>Reset All latches the sync loop off. The latch outranks plugin.reconfigure, which the host fires on every auth file change &mdash; without it the loop would restart and rewrite proxy_url immediately.</li>")
	b.WriteString("<li>The latch is in-memory only: it clears on Resume or process restart. Set enabled=false in the config file to make it stick.</li>")
	b.WriteString("</ul></body></html>")
	return b.String()
}

func row(b *strings.Builder, k, v string) {
	b.WriteString("<tr><th>")
	b.WriteString(html.EscapeString(k))
	b.WriteString("</th><td>")
	b.WriteString(html.EscapeString(v))
	b.WriteString("</td></tr>")
}
