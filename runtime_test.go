package main

import (
	"testing"

	"github.com/ArchmageTony/cpa-resin-sticky-bind/internal/stickybind"
)

func enabledConfig() stickybind.Config {
	cfg := stickybind.Defaults()
	cfg.Enabled = true
	return cfg
}

// reset 会写 auth 文件，host 的 auth watcher 随即下发 plugin.reconfigure。
// 闩锁必须挡住这次覆盖，否则循环重启并把刚清空的 proxy_url 写回。
func TestResetHaltSurvivesReconfigure(t *testing.T) {
	r := &runtime{}
	defer r.Shutdown()

	r.ApplyConfig(enabledConfig())
	if r.stopCh == nil {
		t.Fatal("sync loop should be running before reset")
	}

	cfg := r.HaltForReset()
	if cfg.Enabled {
		t.Fatalf("enabled = %v after halt, want false", cfg.Enabled)
	}
	if r.stopCh != nil {
		t.Fatal("sync loop should be stopped after halt")
	}

	r.ApplyConfig(enabledConfig())
	got, halted := r.State()
	if got.Enabled {
		t.Fatal("reconfigure must not re-enable while the reset latch is set")
	}
	if !halted {
		t.Fatal("halted = false, want true")
	}
	if r.stopCh != nil {
		t.Fatal("reconfigure must not restart the sync loop while halted")
	}
}

func TestResumeRestoresConfiguredState(t *testing.T) {
	r := &runtime{}
	defer r.Shutdown()

	r.ApplyConfig(enabledConfig())
	r.HaltForReset()

	cfg := r.Resume()
	if !cfg.Enabled {
		t.Fatalf("enabled = %v after resume, want true", cfg.Enabled)
	}
	if r.stopCh == nil {
		t.Fatal("sync loop should restart after resume")
	}
	if _, halted := r.State(); halted {
		t.Fatal("halted = true after resume, want false")
	}
}

// 闩锁只叠加 Enabled；配置文件本身禁用时，Resume 不应把它打开。
func TestResumeKeepsConfiguredDisable(t *testing.T) {
	r := &runtime{}
	defer r.Shutdown()

	cfg := stickybind.Defaults()
	cfg.Enabled = false
	r.ApplyConfig(cfg)
	r.HaltForReset()

	got := r.Resume()
	if got.Enabled {
		t.Fatal("resume must not override enabled=false from config")
	}
	if r.stopCh != nil {
		t.Fatal("sync loop must stay stopped when config disables the plugin")
	}
}
