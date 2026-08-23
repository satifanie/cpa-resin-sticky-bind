package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/satifanie/cpa-resin-sticky-bind/internal/stickybind"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type hostAdapter struct{}

func (hostAdapter) ListAuths() ([]stickybind.HostAuthEntry, error) {
	raw, err := callHostResult(pluginabi.MethodHostAuthList, map[string]any{})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Files []stickybind.HostAuthEntry `json:"files"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode host.auth.list: %w", err)
	}
	return resp.Files, nil
}

func (hostAdapter) GetAuth(authIndex string) (stickybind.AuthGetResult, error) {
	raw, err := callHostResult(pluginabi.MethodHostAuthGet, pluginapi.HostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return stickybind.AuthGetResult{}, err
	}
	var resp stickybind.AuthGetResult
	if err := json.Unmarshal(raw, &resp); err != nil {
		return stickybind.AuthGetResult{}, fmt.Errorf("decode host.auth.get: %w", err)
	}
	return resp, nil
}

func (hostAdapter) SaveAuth(name string, rawJSON json.RawMessage) error {
	_, err := callHostResult(pluginabi.MethodHostAuthSave, pluginapi.HostAuthSaveRequest{
		Name: name,
		JSON: rawJSON,
	})
	return err
}

func (hostAdapter) Log(level, message string) {
	hostLog(level, message)
}

type runtime struct {
	mu sync.Mutex
	// cfg 是 host 最近一次下发的配置，不受 reset 影响。
	cfg stickybind.Config
	// resetHalt 是 reset 闩锁：置位后强制停用，直到 Resume 或进程重启。
	resetHalt bool
	stopCh    chan struct{}
	doneCh    chan struct{}
}

var (
	rtOnce sync.Once
	rtInst *runtime
)

func getRuntime() *runtime {
	rtOnce.Do(func() {
		rtInst = &runtime{
			cfg: configDefaults().Normalize(),
		}
	})
	return rtInst
}

func (r *runtime) ApplyConfig(cfg stickybind.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg = cfg.Normalize()
	r.restartLocked()
}

func (r *runtime) Config() stickybind.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.effectiveLocked()
}

// State 返回生效配置与 reset 闩锁状态。
func (r *runtime) State() (stickybind.Config, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.effectiveLocked(), r.resetHalt
}

// effectiveLocked 把 reset 闩锁叠加到 host 下发的配置上。
// 闩锁只影响 Enabled，其余字段仍取自配置，保证 Resume 后能恢复原值。
func (r *runtime) effectiveLocked() stickybind.Config {
	cfg := r.cfg
	if r.resetHalt {
		cfg.Enabled = false
	}
	return cfg
}

// HaltForReset 置位 reset 闩锁并停止同步循环，返回停用后的配置。
//
// 必须用闩锁而非直接改 cfg.Enabled：reset 写 auth 文件会触发 host 的 auth watcher，
// 后者走 syncPluginRuntime -> plugin.reconfigure 重新下发配置文件里的 enabled=true，
// 循环随即重启并把刚清空的 proxy_url 全部写回。
func (r *runtime) HaltForReset() stickybind.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resetHalt = true
	r.restartLocked()
	return r.effectiveLocked()
}

// Resume 清除 reset 闩锁，按 host 下发的配置恢复同步循环。
func (r *runtime) Resume() stickybind.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resetHalt = false
	r.restartLocked()
	return r.effectiveLocked()
}

func (r *runtime) restartLocked() {
	if r.stopCh != nil {
		close(r.stopCh)
		if r.doneCh != nil {
			<-r.doneCh
		}
		r.stopCh = nil
		r.doneCh = nil
	}
	cfg := r.effectiveLocked()
	if !cfg.Enabled {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	r.stopCh = stop
	r.doneCh = done
	go r.loop(cfg, stop, done)
}

func (r *runtime) loop(cfg stickybind.Config, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	b := &stickybind.Binder{
		Cfg:    cfg,
		Host:   hostAdapter{},
		Getenv: os.Getenv,
	}
	run := func() {
		wrote, skipped, unchanged, failed, err := b.SyncOnce()
		if err != nil {
			hostLog("error", "sticky-bind sync error: "+err.Error())
			return
		}
		hostLog("info", fmt.Sprintf("sticky-bind sync wrote=%d skipped=%d unchanged=%d failed=%d", wrote, skipped, unchanged, failed))
	}
	// initial pass shortly after start
	select {
	case <-stop:
		return
	case <-time.After(2 * time.Second):
		run()
	}
	ticker := time.NewTicker(time.Duration(cfg.SyncIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// 不在此处回读 r.Config()：配置变更必经 ApplyConfig 重启 loop，
			// 且回读会与持锁等待 goroutine 退出的 restartLocked 死锁。
			run()
		}
	}
}

func (r *runtime) Shutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopCh != nil {
		close(r.stopCh)
		if r.doneCh != nil {
			<-r.doneCh
		}
		r.stopCh = nil
		r.doneCh = nil
	}
}
