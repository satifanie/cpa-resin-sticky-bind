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
	mu     sync.Mutex
	cfg    stickybind.Config
	stopCh chan struct{}
	doneCh chan struct{}
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
	return r.cfg
}

// Disable 关闭 enabled 并停止同步循环，返回更新后的配置。
// 仅作用于进程内状态：host 未提供写回插件配置的回调，
// 下次 plugin.reconfigure 会按配置文件恢复 enabled。
func (r *runtime) Disable() stickybind.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg.Enabled = false
	r.restartLocked()
	return r.cfg
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
	if !r.cfg.Enabled {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	r.stopCh = stop
	r.doneCh = done
	cfg := r.cfg
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
