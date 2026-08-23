package stickybind

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// HostAuthEntry is one credential from host.auth.list.
type HostAuthEntry struct {
	ID          string `json:"id,omitempty"`
	AuthIndex   string `json:"auth_index,omitempty"`
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Label       string `json:"label,omitempty"`
	Status      string `json:"status,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
	Unavailable bool   `json:"unavailable,omitempty"`
	RuntimeOnly bool   `json:"runtime_only,omitempty"`
	Source      string `json:"source,omitempty"`
	Path        string `json:"path,omitempty"`
	Email       string `json:"email,omitempty"`
}

// AuthGetResult is host.auth.get payload.
type AuthGetResult struct {
	AuthIndex string          `json:"auth_index"`
	Name      string          `json:"name,omitempty"`
	Path      string          `json:"path,omitempty"`
	JSON      json.RawMessage `json:"json"`
}

// Action describes one sync decision.
type Action string

const (
	ActionSkip      Action = "skip"
	ActionWrite     Action = "write"
	ActionUnchanged Action = "unchanged"
	ActionClear     Action = "clear"
)

// Decision is the result of evaluating one credential.
type Decision struct {
	Action     Action
	Reason     string
	AuthIndex  string
	Name       string
	Provider   string
	Platform   string
	Account    string
	ProxyURL   string
	SaveName   string
	SaveJSON   json.RawMessage
	Redacted   string
}

// HostAPI is the minimal host surface used by the binder.
type HostAPI interface {
	ListAuths() ([]HostAuthEntry, error)
	GetAuth(authIndex string) (AuthGetResult, error)
	SaveAuth(name string, rawJSON json.RawMessage) error
	Log(level, message string)
}

// Binder performs credential proxy binding.
type Binder struct {
	Cfg    Config
	Host   HostAPI
	Getenv func(string) string
}

// SyncOnce enumerates credentials and binds empty proxy_url fields.
func (b *Binder) SyncOnce() (wrote, skipped, unchanged, failed int, err error) {
	if b == nil || b.Host == nil {
		return 0, 0, 0, 0, fmt.Errorf("binder host is nil")
	}
	cfg := b.Cfg.Normalize()
	if !cfg.Enabled {
		b.Host.Log("info", "sticky-bind disabled; skip sync")
		return 0, 0, 0, 0, nil
	}
	proxy, errProxy := ParseResinProxyURL(cfg.ResinProxyURL, cfg.ProxyTokenEnv, b.Getenv)
	if errProxy != nil {
		return 0, 0, 0, 0, errProxy
	}

	entries, errList := b.Host.ListAuths()
	if errList != nil {
		return 0, 0, 0, 0, errList
	}
	for _, entry := range entries {
		dec, errDecide := b.decide(cfg, proxy, entry)
		if errDecide != nil {
			failed++
			b.Host.Log("error", fmt.Sprintf("decide failed name=%s auth_index=%s err=%v", entry.Name, entry.AuthIndex, errDecide))
			continue
		}
		switch dec.Action {
		case ActionSkip:
			skipped++
			b.Host.Log("info", fmt.Sprintf("skip name=%s auth_index=%s reason=%s", dec.Name, dec.AuthIndex, dec.Reason))
		case ActionUnchanged:
			unchanged++
			b.Host.Log("info", fmt.Sprintf("unchanged name=%s auth_index=%s platform=%s account=%s proxy=%s", dec.Name, dec.AuthIndex, dec.Platform, dec.Account, dec.Redacted))
		case ActionWrite:
			if errSave := b.Host.SaveAuth(dec.SaveName, dec.SaveJSON); errSave != nil {
				failed++
				b.Host.Log("error", fmt.Sprintf("save failed name=%s auth_index=%s err=%v", dec.Name, dec.AuthIndex, errSave))
				continue
			}
			wrote++
			b.Host.Log("info", fmt.Sprintf("bound name=%s auth_index=%s platform=%s account=%s proxy=%s", dec.Name, dec.AuthIndex, dec.Platform, dec.Account, dec.Redacted))
		}
	}
	return wrote, skipped, unchanged, failed, nil
}

// ResetOnce 清除由本插件按当前规则生成的 proxy_url。
// 手工配置的 proxy_url 与规则生成值不一致，会被跳过而非清空。
// 不检查 cfg.Enabled：禁用插件后仍需能清理残留绑定。
func (b *Binder) ResetOnce() (cleared, skipped, failed int, err error) {
	if b == nil || b.Host == nil {
		return 0, 0, 0, fmt.Errorf("binder host is nil")
	}
	cfg := b.Cfg.Normalize()
	proxy, errProxy := ParseResinProxyURL(cfg.ResinProxyURL, cfg.ProxyTokenEnv, b.Getenv)
	if errProxy != nil {
		return 0, 0, 0, errProxy
	}

	entries, errList := b.Host.ListAuths()
	if errList != nil {
		return 0, 0, 0, errList
	}
	for _, entry := range entries {
		dec, errDecide := b.decideReset(cfg, proxy, entry)
		if errDecide != nil {
			failed++
			b.Host.Log("error", fmt.Sprintf("reset decide failed name=%s auth_index=%s err=%v", entry.Name, entry.AuthIndex, errDecide))
			continue
		}
		if dec.Action != ActionClear {
			skipped++
			b.Host.Log("info", fmt.Sprintf("reset skip name=%s auth_index=%s reason=%s", dec.Name, dec.AuthIndex, dec.Reason))
			continue
		}
		if errSave := b.Host.SaveAuth(dec.SaveName, dec.SaveJSON); errSave != nil {
			failed++
			b.Host.Log("error", fmt.Sprintf("reset save failed name=%s auth_index=%s err=%v", dec.Name, dec.AuthIndex, errSave))
			continue
		}
		cleared++
		b.Host.Log("info", fmt.Sprintf("reset cleared name=%s auth_index=%s platform=%s account=%s was=%s", dec.Name, dec.AuthIndex, dec.Platform, dec.Account, dec.Redacted))
	}
	return cleared, skipped, failed, nil
}

// authContext 是一条 credential 经公共过滤与解析后的中间结果。
type authContext struct {
	AuthIndex     string
	Name          string // 规范化后的保存文件名
	Provider      string
	Meta          map[string]any
	ExistingProxy string
}

// prepare 执行 sync 与 reset 共用的过滤和解析。
// 返回的 skip 非 nil 时表示该 credential 应直接跳过。
func (b *Binder) prepare(cfg Config, entry HostAuthEntry) (authContext, *Decision, error) {
	ctx := authContext{
		AuthIndex: strings.TrimSpace(entry.AuthIndex),
		Name:      strings.TrimSpace(entry.Name),
		Provider:  firstNonEmpty(entry.Provider, entry.Type),
	}
	if ctx.AuthIndex == "" {
		return ctx, &Decision{Action: ActionSkip, Reason: "missing_auth_index", Name: ctx.Name}, nil
	}
	if entry.RuntimeOnly {
		return ctx, &Decision{Action: ActionSkip, Reason: "runtime_only", AuthIndex: ctx.AuthIndex, Name: ctx.Name, Provider: ctx.Provider}, nil
	}
	if entry.Disabled || strings.EqualFold(entry.Status, "disabled") {
		return ctx, &Decision{Action: ActionSkip, Reason: "disabled", AuthIndex: ctx.AuthIndex, Name: ctx.Name, Provider: ctx.Provider}, nil
	}
	if !ProviderAllowed(cfg, ctx.Provider) {
		return ctx, &Decision{Action: ActionSkip, Reason: "provider_filtered", AuthIndex: ctx.AuthIndex, Name: ctx.Name, Provider: ctx.Provider}, nil
	}

	got, errGet := b.Host.GetAuth(ctx.AuthIndex)
	if errGet != nil {
		return ctx, nil, errGet
	}
	saveName := firstNonEmpty(strings.TrimSpace(got.Name), ctx.Name)
	if saveName == "" {
		return ctx, &Decision{Action: ActionSkip, Reason: "missing_filename", AuthIndex: ctx.AuthIndex, Name: ctx.Name, Provider: ctx.Provider}, nil
	}
	if !strings.HasSuffix(strings.ToLower(saveName), ".json") {
		saveName = saveName + ".json"
	}
	ctx.Name = filepath.Base(saveName)

	meta, existingProxy, errMeta := parseAuthJSON(got.JSON)
	if errMeta != nil {
		return ctx, nil, errMeta
	}
	ctx.Meta = meta
	ctx.ExistingProxy = existingProxy
	return ctx, nil, nil
}

// resolveTarget 按当前配置推导该 credential 应有的 Platform/Account/proxy_url。
// platform 为空表示配置未解析出 Platform，由调用方按 platform_empty 处理。
func resolveTarget(cfg Config, proxy ParsedProxy, entry HostAuthEntry, ctx authContext) (platform, account, proxyURL string, err error) {
	authID := firstNonEmpty(entry.ID, metaString(ctx.Meta, "id"), ctx.Name)
	platform = ResolvePlatform(cfg, authID, ctx.Provider)
	if platform == "" {
		return "", "", "", nil
	}
	account = BuildAccount(cfg, AccountInput{
		AuthID:   authID,
		Email:    firstNonEmpty(entry.Email, metaString(ctx.Meta, "email")),
		Sub:      metaString(ctx.Meta, "sub"),
		Filename: ctx.Name,
		Provider: ctx.Provider,
	})
	proxyURL, err = BuildProxyURL(proxy, platform, account)
	if err != nil {
		return "", "", "", err
	}
	return platform, account, proxyURL, nil
}

func (b *Binder) decide(cfg Config, proxy ParsedProxy, entry HostAuthEntry) (Decision, error) {
	ctx, skip, errPrepare := b.prepare(cfg, entry)
	if errPrepare != nil {
		return Decision{}, errPrepare
	}
	if skip != nil {
		return *skip, nil
	}
	if ctx.ExistingProxy != "" && cfg.OnlyIfEmpty && !cfg.OverwriteExisting {
		platform, account, _ := ExtractAccountFromProxyURL(ctx.ExistingProxy)
		return Decision{
			Action:    ActionSkip,
			Reason:    "proxy_url_present",
			AuthIndex: ctx.AuthIndex,
			Name:      ctx.Name,
			Provider:  ctx.Provider,
			Platform:  platform,
			Account:   account,
			ProxyURL:  ctx.ExistingProxy,
			Redacted:  RedactProxyURL(ctx.ExistingProxy),
		}, nil
	}

	platform, account, proxyURL, errBuild := resolveTarget(cfg, proxy, entry, ctx)
	if errBuild != nil {
		return Decision{}, errBuild
	}
	if platform == "" {
		return Decision{Action: ActionSkip, Reason: "platform_empty", AuthIndex: ctx.AuthIndex, Name: ctx.Name, Provider: ctx.Provider}, nil
	}
	if ctx.ExistingProxy != "" && sameProxyURL(ctx.ExistingProxy, proxyURL) {
		return Decision{
			Action:    ActionUnchanged,
			Reason:    "already_bound",
			AuthIndex: ctx.AuthIndex,
			Name:      ctx.Name,
			Provider:  ctx.Provider,
			Platform:  platform,
			Account:   account,
			ProxyURL:  ctx.ExistingProxy,
			Redacted:  RedactProxyURL(ctx.ExistingProxy),
		}, nil
	}

	ctx.Meta["proxy_url"] = proxyURL
	raw, errMarshal := json.Marshal(ctx.Meta)
	if errMarshal != nil {
		return Decision{}, errMarshal
	}
	return Decision{
		Action:    ActionWrite,
		Reason:    "bind",
		AuthIndex: ctx.AuthIndex,
		Name:      ctx.Name,
		Provider:  ctx.Provider,
		Platform:  platform,
		Account:   account,
		ProxyURL:  proxyURL,
		SaveName:  ctx.Name,
		SaveJSON:  raw,
		Redacted:  RedactProxyURL(proxyURL),
	}, nil
}

// decideReset 判断一条 credential 的 proxy_url 是否由本插件生成，是则置空。
func (b *Binder) decideReset(cfg Config, proxy ParsedProxy, entry HostAuthEntry) (Decision, error) {
	ctx, skip, errPrepare := b.prepare(cfg, entry)
	if errPrepare != nil {
		return Decision{}, errPrepare
	}
	if skip != nil {
		return *skip, nil
	}
	if ctx.ExistingProxy == "" {
		return Decision{Action: ActionSkip, Reason: "proxy_url_empty", AuthIndex: ctx.AuthIndex, Name: ctx.Name, Provider: ctx.Provider}, nil
	}

	platform, account, expected, errBuild := resolveTarget(cfg, proxy, entry, ctx)
	if errBuild != nil {
		return Decision{}, errBuild
	}
	if platform == "" {
		return Decision{Action: ActionSkip, Reason: "platform_empty", AuthIndex: ctx.AuthIndex, Name: ctx.Name, Provider: ctx.Provider}, nil
	}
	// 忽略密码比对：token 轮换后已写入值的密码会与当前配置不同，
	// 但 Platform.Account 是插件独有的命名格式，足以判定归属。
	if !sameProxyIdentity(ctx.ExistingProxy, expected) {
		return Decision{
			Action:    ActionSkip,
			Reason:    "not_plugin_managed",
			AuthIndex: ctx.AuthIndex,
			Name:      ctx.Name,
			Provider:  ctx.Provider,
			ProxyURL:  ctx.ExistingProxy,
			Redacted:  RedactProxyURL(ctx.ExistingProxy),
		}, nil
	}

	ctx.Meta["proxy_url"] = ""
	raw, errMarshal := json.Marshal(ctx.Meta)
	if errMarshal != nil {
		return Decision{}, errMarshal
	}
	return Decision{
		Action:    ActionClear,
		Reason:    "reset",
		AuthIndex: ctx.AuthIndex,
		Name:      ctx.Name,
		Provider:  ctx.Provider,
		Platform:  platform,
		Account:   account,
		ProxyURL:  "",
		SaveName:  ctx.Name,
		SaveJSON:  raw,
		Redacted:  RedactProxyURL(ctx.ExistingProxy),
	}, nil
}

func parseAuthJSON(raw json.RawMessage) (map[string]any, string, error) {
	if len(bytesTrimSpace(raw)) == 0 {
		return nil, "", fmt.Errorf("auth json is empty")
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, "", fmt.Errorf("invalid auth json: %w", err)
	}
	if meta == nil {
		meta = map[string]any{}
	}
	proxy := strings.TrimSpace(metaString(meta, "proxy_url"))
	return meta, proxy, nil
}

func metaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	if v, ok := meta[key]; ok {
		return StringFromAny(v)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func bytesTrimSpace(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}

func sameProxyURL(a, b string) bool {
	return normalizeProxyCompare(a) == normalizeProxyCompare(b)
}

// sameProxyIdentity 忽略密码比较代理身份：scheme + Platform.Account + host:port。
// 用于 reset 判定归属，使 token 轮换后仍能清除插件写入的值。
func sameProxyIdentity(a, b string) bool {
	idA, okA := proxyIdentity(a)
	if !okA {
		return false
	}
	idB, okB := proxyIdentity(b)
	if !okB {
		return false
	}
	return idA == idB
}

func proxyIdentity(raw string) (string, bool) {
	u, err := parseLooseURL(raw)
	if err != nil || u.User == nil {
		return "", false
	}
	user := strings.TrimSpace(u.User.Username())
	if user == "" {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + user + "@" + strings.ToLower(u.Host), true
}

func normalizeProxyCompare(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	// compare full string with password included; equality is exact after trim
	return u.String()
}

func parseLooseURL(raw string) (*url.URL, error) {
	return url.Parse(strings.TrimSpace(raw))
}
