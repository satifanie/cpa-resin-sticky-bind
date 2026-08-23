package stickybind

import (
	"encoding/json"
	"fmt"
	"testing"
)

type mockHost struct {
	entries []HostAuthEntry
	files   map[string]json.RawMessage // auth_index -> json
	names   map[string]string
	saved   map[string]json.RawMessage
	logs    []string
}

func (m *mockHost) ListAuths() ([]HostAuthEntry, error) { return m.entries, nil }
func (m *mockHost) GetAuth(authIndex string) (AuthGetResult, error) {
	raw, ok := m.files[authIndex]
	if !ok {
		return AuthGetResult{}, fmt.Errorf("missing %s", authIndex)
	}
	return AuthGetResult{
		AuthIndex: authIndex,
		Name:      m.names[authIndex],
		JSON:      raw,
	}, nil
}
func (m *mockHost) SaveAuth(name string, rawJSON json.RawMessage) error {
	if m.saved == nil {
		m.saved = map[string]json.RawMessage{}
	}
	m.saved[name] = append(json.RawMessage(nil), rawJSON...)
	// update underlying by name match
	for idx, n := range m.names {
		if n == name {
			m.files[idx] = append(json.RawMessage(nil), rawJSON...)
		}
	}
	return nil
}
func (m *mockHost) Log(level, message string) {
	m.logs = append(m.logs, level+":"+message)
}

func TestSyncOnceWritesEmptyProxy(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID:        "xai-a.json",
			AuthIndex: "idx1",
			Name:      "xai-a.json",
			Provider:  "xai",
			Email:     "a@example.com",
		}},
		files: map[string]json.RawMessage{
			"idx1": json.RawMessage(`{"type":"xai","email":"a@example.com","access_token":"t"}`),
		},
		names: map[string]string{"idx1": "xai-a.json"},
	}
	b := &Binder{
		Cfg: Defaults(),
		Host: host,
		Getenv: func(string) string { return "tok" },
	}
	wrote, skipped, unchanged, failed, err := b.SyncOnce()
	if err != nil {
		t.Fatal(err)
	}
	if wrote != 1 || skipped != 0 || unchanged != 0 || failed != 0 {
		t.Fatalf("stats wrote=%d skipped=%d unchanged=%d failed=%d", wrote, skipped, unchanged, failed)
	}
	raw := host.saved["xai-a.json"]
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	proxy, _ := meta["proxy_url"].(string)
	wantPrefix := "socks5h://default."
	if proxy == "" || len(proxy) < len(wantPrefix) || proxy[:len(wantPrefix)] != wantPrefix {
		t.Fatalf("proxy = %q, want prefix %q", proxy, wantPrefix)
	}
	if RedactProxyURL(proxy) == proxy {
		t.Fatalf("should redact: %q", proxy)
	}
}

func TestSyncOnceSkipsExistingProxy(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "xai-b.json", AuthIndex: "idx2", Name: "xai-b.json", Provider: "xai",
		}},
		files: map[string]json.RawMessage{
			"idx2": json.RawMessage(`{"type":"xai","proxy_url":"socks5h://default.custom:tok@resin:2260"}`),
		},
		names: map[string]string{"idx2": "xai-b.json"},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "tok" }}
	wrote, skipped, _, failed, err := b.SyncOnce()
	if err != nil {
		t.Fatal(err)
	}
	if wrote != 0 || skipped != 1 || failed != 0 {
		t.Fatalf("wrote=%d skipped=%d failed=%d", wrote, skipped, failed)
	}
	if len(host.saved) != 0 {
		t.Fatalf("unexpected save: %#v", host.saved)
	}
}

func TestSyncOnceIdempotentSecondPass(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "id1", AuthIndex: "idx3", Name: "xai-c.json", Provider: "xai", Email: "c@example.com",
		}},
		files: map[string]json.RawMessage{
			"idx3": json.RawMessage(`{"type":"xai","email":"c@example.com"}`),
		},
		names: map[string]string{"idx3": "xai-c.json"},
	}
	cfg := Defaults()
	cfg.OnlyIfEmpty = false
	cfg.OverwriteExisting = true
	b := &Binder{Cfg: cfg, Host: host, Getenv: func(string) string { return "tok" }}
	if _, _, _, _, err := b.SyncOnce(); err != nil {
		t.Fatal(err)
	}
	wrote, skipped, unchanged, failed, err := b.SyncOnce()
	if err != nil {
		t.Fatal(err)
	}
	if wrote != 0 || failed != 0 {
		t.Fatalf("second pass wrote=%d failed=%d skipped=%d unchanged=%d", wrote, failed, skipped, unchanged)
	}
	if unchanged != 1 && skipped != 1 {
		// with overwrite true and same URL -> unchanged
		t.Fatalf("expected unchanged/skip, got wrote=%d skipped=%d unchanged=%d", wrote, skipped, unchanged)
	}
}

func TestSyncOnceSkipsRuntimeOnlyAndDisabled(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{
			{AuthIndex: "r1", Name: "rt.json", RuntimeOnly: true, Provider: "xai"},
			{AuthIndex: "d1", Name: "dis.json", Disabled: true, Provider: "xai"},
		},
		files: map[string]json.RawMessage{},
		names: map[string]string{},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "tok" }}
	wrote, skipped, _, failed, err := b.SyncOnce()
	if err != nil {
		t.Fatal(err)
	}
	if wrote != 0 || skipped != 2 || failed != 0 {
		t.Fatalf("wrote=%d skipped=%d failed=%d", wrote, skipped, failed)
	}
}

func TestResetOnceClearsPluginManagedProxy(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "xai-a.json", AuthIndex: "idx1", Name: "xai-a.json", Provider: "xai", Email: "a@example.com",
		}},
		files: map[string]json.RawMessage{
			"idx1": json.RawMessage(`{"type":"xai","email":"a@example.com","access_token":"t"}`),
		},
		names: map[string]string{"idx1": "xai-a.json"},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "tok" }}
	if wrote, _, _, _, err := b.SyncOnce(); err != nil || wrote != 1 {
		t.Fatalf("setup sync wrote=%d err=%v", wrote, err)
	}

	cleared, skipped, failed, err := b.ResetOnce()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 1 || skipped != 0 || failed != 0 {
		t.Fatalf("cleared=%d skipped=%d failed=%d", cleared, skipped, failed)
	}
	var meta map[string]any
	if err := json.Unmarshal(host.saved["xai-a.json"], &meta); err != nil {
		t.Fatal(err)
	}
	if proxy, _ := meta["proxy_url"].(string); proxy != "" {
		t.Fatalf("proxy_url = %q, want empty", proxy)
	}
	if meta["access_token"] != "t" {
		t.Fatalf("other fields lost: %#v", meta)
	}
}

func TestResetOnceKeepsManualProxy(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "xai-b.json", AuthIndex: "idx2", Name: "xai-b.json", Provider: "xai",
		}},
		files: map[string]json.RawMessage{
			"idx2": json.RawMessage(`{"type":"xai","proxy_url":"socks5h://user:pass@other-host:1080"}`),
		},
		names: map[string]string{"idx2": "xai-b.json"},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "tok" }}
	cleared, skipped, failed, err := b.ResetOnce()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 0 || skipped != 1 || failed != 0 {
		t.Fatalf("cleared=%d skipped=%d failed=%d", cleared, skipped, failed)
	}
	if len(host.saved) != 0 {
		t.Fatalf("unexpected save: %#v", host.saved)
	}
}

func TestResetOnceSkipsEmptyProxy(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "xai-c.json", AuthIndex: "idx3", Name: "xai-c.json", Provider: "xai",
		}},
		files: map[string]json.RawMessage{
			"idx3": json.RawMessage(`{"type":"xai"}`),
		},
		names: map[string]string{"idx3": "xai-c.json"},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "tok" }}
	cleared, skipped, failed, err := b.ResetOnce()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 0 || skipped != 1 || failed != 0 {
		t.Fatalf("cleared=%d skipped=%d failed=%d", cleared, skipped, failed)
	}
}

// token 轮换后已写入值的密码与当前配置不同，仍应能清除
func TestResetOnceClearsAfterTokenRotation(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "xai-e.json", AuthIndex: "idx5", Name: "xai-e.json", Provider: "xai",
		}},
		files: map[string]json.RawMessage{
			"idx5": json.RawMessage(`{"type":"xai"}`),
		},
		names: map[string]string{"idx5": "xai-e.json"},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "old-token" }}
	if wrote, _, _, _, err := b.SyncOnce(); err != nil || wrote != 1 {
		t.Fatalf("setup sync wrote=%d err=%v", wrote, err)
	}

	b.Getenv = func(string) string { return "new-token" }
	cleared, skipped, failed, err := b.ResetOnce()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 1 || skipped != 0 || failed != 0 {
		t.Fatalf("cleared=%d skipped=%d failed=%d", cleared, skipped, failed)
	}
}

// Platform.Account 不匹配的值属于其他来源，不能清除
func TestResetOnceKeepsDifferentAccount(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "xai-f.json", AuthIndex: "idx6", Name: "xai-f.json", Provider: "xai",
		}},
		files: map[string]json.RawMessage{
			"idx6": json.RawMessage(`{"type":"xai","proxy_url":"socks5h://default.someone-else:tok@resin:2260"}`),
		},
		names: map[string]string{"idx6": "xai-f.json"},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "tok" }}
	cleared, skipped, failed, err := b.ResetOnce()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 0 || skipped != 1 || failed != 0 {
		t.Fatalf("cleared=%d skipped=%d failed=%d", cleared, skipped, failed)
	}
	if len(host.saved) != 0 {
		t.Fatalf("unexpected save: %#v", host.saved)
	}
}

// 禁用插件后仍需能清理已写入的绑定
func TestResetOnceRunsWhenDisabled(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "xai-d.json", AuthIndex: "idx4", Name: "xai-d.json", Provider: "xai",
		}},
		files: map[string]json.RawMessage{
			"idx4": json.RawMessage(`{"type":"xai"}`),
		},
		names: map[string]string{"idx4": "xai-d.json"},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "tok" }}
	if wrote, _, _, _, err := b.SyncOnce(); err != nil || wrote != 1 {
		t.Fatalf("setup sync wrote=%d err=%v", wrote, err)
	}

	cfg := Defaults()
	cfg.Enabled = false
	b.Cfg = cfg
	cleared, _, failed, err := b.ResetOnce()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 1 || failed != 0 {
		t.Fatalf("cleared=%d failed=%d", cleared, failed)
	}
}

// 凭据在绑定后被禁用，reset 仍需清理其残留绑定
func TestResetOnceClearsDisabledCredential(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "xai-g.json", AuthIndex: "idx7", Name: "xai-g.json", Provider: "xai",
		}},
		files: map[string]json.RawMessage{
			"idx7": json.RawMessage(`{"type":"xai"}`),
		},
		names: map[string]string{"idx7": "xai-g.json"},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "tok" }}
	if wrote, _, _, _, err := b.SyncOnce(); err != nil || wrote != 1 {
		t.Fatalf("setup sync wrote=%d err=%v", wrote, err)
	}

	host.entries[0].Disabled = true
	cleared, skipped, failed, err := b.ResetOnce()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 1 || skipped != 0 || failed != 0 {
		t.Fatalf("cleared=%d skipped=%d failed=%d", cleared, skipped, failed)
	}
	var meta map[string]any
	if err := json.Unmarshal(host.saved["xai-g.json"], &meta); err != nil {
		t.Fatal(err)
	}
	if proxy, _ := meta["proxy_url"].(string); proxy != "" {
		t.Fatalf("proxy_url = %q, want empty", proxy)
	}
}

// provider 过滤改动后，先前绑定的凭据 reset 仍需清理
func TestResetOnceClearsProviderFilteredCredential(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "xai-h.json", AuthIndex: "idx8", Name: "xai-h.json", Provider: "xai",
		}},
		files: map[string]json.RawMessage{
			"idx8": json.RawMessage(`{"type":"xai"}`),
		},
		names: map[string]string{"idx8": "xai-h.json"},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "tok" }}
	if wrote, _, _, _, err := b.SyncOnce(); err != nil || wrote != 1 {
		t.Fatalf("setup sync wrote=%d err=%v", wrote, err)
	}

	cfg := Defaults()
	cfg.ExcludeProviders = []string{"xai"}
	b.Cfg = cfg
	if wrote, skipped, _, _, err := b.SyncOnce(); err != nil || wrote != 0 || skipped != 1 {
		t.Fatalf("sync should filter: wrote=%d skipped=%d err=%v", wrote, skipped, err)
	}
	cleared, skipped, failed, err := b.ResetOnce()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 1 || skipped != 0 || failed != 0 {
		t.Fatalf("cleared=%d skipped=%d failed=%d", cleared, skipped, failed)
	}
}

// runtime_only 凭据无法通过 SaveAuth 持久化，reset 也必须跳过
func TestResetOnceSkipsRuntimeOnly(t *testing.T) {
	host := &mockHost{
		entries: []HostAuthEntry{{
			ID: "rt.json", AuthIndex: "idx9", Name: "rt.json", Provider: "xai", RuntimeOnly: true,
		}},
		files: map[string]json.RawMessage{
			"idx9": json.RawMessage(`{"type":"xai","proxy_url":"socks5h://default.rt.json:tok@resin:2260"}`),
		},
		names: map[string]string{"idx9": "rt.json"},
	}
	b := &Binder{Cfg: Defaults(), Host: host, Getenv: func(string) string { return "tok" }}
	cleared, skipped, failed, err := b.ResetOnce()
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 0 || skipped != 1 || failed != 0 {
		t.Fatalf("cleared=%d skipped=%d failed=%d", cleared, skipped, failed)
	}
	if len(host.saved) != 0 {
		t.Fatalf("unexpected save: %#v", host.saved)
	}
}
