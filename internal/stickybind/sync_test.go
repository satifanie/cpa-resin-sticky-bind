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
