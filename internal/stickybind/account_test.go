package stickybind

import "testing"

func TestBuildAccountAuthIDPreferred(t *testing.T) {
	cfg := Defaults()
	got := BuildAccount(cfg, AccountInput{
		AuthID:   "AbC-123",
		Email:    "user@example.com",
		Filename: "xai-user@example.com.json",
	})
	if got != "abc-123" {
		t.Fatalf("account = %q, want abc-123", got)
	}
}

func TestBuildAccountEmailFallbackAndSanitize(t *testing.T) {
	cfg := Defaults()
	got := BuildAccount(cfg, AccountInput{
		Email:    "User+Tag@Example.COM",
		Filename: "file.json",
	})
	if got != "user_tag_example.com" {
		t.Fatalf("account = %q", got)
	}
}

func TestBuildAccountTruncatesWithHash(t *testing.T) {
	cfg := Defaults()
	long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	got := BuildAccount(cfg, AccountInput{AuthID: long})
	if len(got) > MaxAccountLen {
		t.Fatalf("len=%d > %d: %q", len(got), MaxAccountLen, got)
	}
	if got == "" {
		t.Fatal("empty account")
	}
}

func TestBuildAccountPrefixAndIdempotent(t *testing.T) {
	cfg := Defaults()
	cfg.AccountPrefix = "cpa_"
	in := AccountInput{AuthID: "id-1", Email: "a@b.com"}
	a := BuildAccount(cfg, in)
	b := BuildAccount(cfg, in)
	if a != b {
		t.Fatalf("not idempotent: %q vs %q", a, b)
	}
	if a != "cpa_id-1" {
		t.Fatalf("account = %q", a)
	}
}

func TestResolvePlatformPriority(t *testing.T) {
	cfg := Defaults()
	cfg.DefaultPlatform = "default"
	cfg.PlatformByProvider = map[string]string{"xai": "pool-a"}
	cfg.PlatformByAuthID = map[string]string{"auth-1": "pool-b"}
	if got := ResolvePlatform(cfg, "auth-1", "xai"); got != "pool-b" {
		t.Fatalf("got %q want pool-b", got)
	}
	if got := ResolvePlatform(cfg, "auth-2", "xai"); got != "pool-a" {
		t.Fatalf("got %q want pool-a", got)
	}
	if got := ResolvePlatform(cfg, "auth-2", "claude"); got != "default" {
		t.Fatalf("got %q want default", got)
	}
}

func TestProviderAllowed(t *testing.T) {
	cfg := Defaults()
	cfg.IncludeProviders = []string{"xai"}
	cfg.ExcludeProviders = []string{"demo"}
	if !ProviderAllowed(cfg, "xai") {
		t.Fatal("xai should be allowed")
	}
	if ProviderAllowed(cfg, "claude") {
		t.Fatal("claude should be filtered by include")
	}
	cfg = Defaults()
	cfg.ExcludeProviders = []string{"xai"}
	if ProviderAllowed(cfg, "xai") {
		t.Fatal("xai should be excluded")
	}
}
