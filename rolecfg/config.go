// Package rolecfg loads and validates provider-neutral agent role manifests.
package rolecfg

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/netbja/agent-bus-monitor/models"
)

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	envPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	// sessionIDPattern is a session id the launcher passes to --resume: never
	// dash-led (a flag), never a path, bounded.
	sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// forbiddenEnvRefs are the targets env_refs may never set: the launcher's identity
// and process controls, and every variable that changes how the client or the
// tools it runs load code (dynamic linker, shell, interpreter and git hooks).
var forbiddenEnvRefs = map[string]bool{
	"PATH": true, "HOME": true, "ROLES_TOML": true, "CLAUDE_CODE_SESSION_ID": true,
	"BASH_ENV": true, "ENV": true, "SHELLOPTS": true, "PROMPT_COMMAND": true, "IFS": true,
	"NODE_OPTIONS": true, "NODE_PATH": true, "GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_EXEC_PATH": true,
	"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "PERL5OPT": true, "PERL5LIB": true,
	"RUBYOPT": true, "RUBYLIB": true, "GIT_DIR": true, "GIT_TEMPLATE_DIR": true, "GIT_ASKPASS": true,
	"SSH_ASKPASS": true, "GIT_PROXY_COMMAND": true, "GIT_EXTERNAL_DIFF": true, "GIT_EDITOR": true,
	"GIT_PAGER": true, "EDITOR": true, "VISUAL": true, "PAGER": true,
}

// GIT_CONFIG covers GIT_CONFIG_COUNT/KEY_n/VALUE_n and GIT_CONFIG_PARAMETERS, which set any git
// option (core.sshCommand, core.hooksPath) and so bypass the names above.
var forbiddenEnvRefPrefixes = []string{"AGENT_BUS_", "AGENT_LAUNCH_", "LD_", "DYLD_", "HERDR_", "PYTHON", "GIT_CONFIG"}

func envRefForbidden(key string) bool {
	if forbiddenEnvRefs[key] {
		return true
	}
	for _, p := range forbiddenEnvRefPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// Manifest is a validated role configuration. Its maps contain decoded TOML
// values; roleOrder preserves the declaration order used by Python's dicts.
type Manifest struct {
	SchemaVersion   int
	Defaults        map[string]interface{}
	Profiles        map[string]interface{}
	Roles           map[string]interface{}
	Adapters        map[string]interface{}
	SharedToolRules ToolRules
	manifestPath    string
	roleOrder       []string
	profileOrder    []string
}

// Profile is the resolved, client-ready configuration for one role.
type Profile struct {
	Client          string                 `json:"client"`
	Provider        string                 `json:"provider"`
	Model           string                 `json:"model"`
	Tier            string                 `json:"tier,omitempty"` // v0.15.2: resolved to Model at launch
	Reasoning       string                 `json:"reasoning,omitempty"`
	ReasoningLevels []string               `json:"reasoning_levels,omitempty"`
	Options         map[string]string      `json:"-"`
	OptionsRaw      map[string]interface{} `json:"options"`
	EnvRefs         map[string]string      `json:"env_refs,omitempty"`
	Profile         string                 `json:"profile"`
	Role            string                 `json:"role"`
	Legacy          bool                   `json:"legacy"`
	Skills          []string               `json:"skills"`
	Manifest        string                 `json:"manifest"`
	Adapter         string                 `json:"adapter,omitempty"`
	Pops            bool                   `json:"pops,omitempty"`      // v0.17: pops other roles; spawn rules derived
	Approval        string                 `json:"approval,omitempty"`  // v0.17: "person" = a popping role's session asks before agent-spawn
	ToolRules       ToolRules              `json:"tool_rules,omitzero"` // v0.17: declared Claude Code rules
}

// Load reads, validates, and resolves every role in a manifest. Callers should
// pass an absolute path, as the launcher does after resolving ROLES_TOML.
func Load(path string) (*Manifest, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	var raw map[string]interface{}
	meta, err := toml.DecodeFile(abs, &raw)
	if err != nil {
		return nil, err
	}
	version := 1
	if v, exists := raw["schema_version"]; exists {
		n, ok := v.(int64)
		if !ok || (n != 1 && n != 2) {
			return nil, fmt.Errorf("unsupported schema_version (expected 1 or 2)")
		}
		version = int(n)
	}
	roles, ok := asTable(raw["roles"])
	if !ok {
		return nil, fmt.Errorf("roles must be a table")
	}
	m := &Manifest{
		SchemaVersion: version,
		Roles:         roles,
		manifestPath:  abs,
	}
	if sharedRaw, exists := raw["tool_rules"]; exists {
		m.SharedToolRules, err = parseToolRules("manifest tool_rules", sharedRaw)
		if err != nil {
			return nil, err
		}
	}
	for _, key := range meta.Keys() {
		parts := []string(key)
		if len(parts) >= 2 {
			switch parts[0] {
			case "roles":
				m.roleOrder = appendUnique(m.roleOrder, parts[1])
			case "profiles":
				m.profileOrder = appendUnique(m.profileOrder, parts[1])
			}
		}
	}
	// Inline tables do not always produce group keys in the same form. Add
	// any decoded names not already represented, preserving TOML order where
	// the decoder supplies it and sorting only the otherwise unordered tail.
	appendMissingSorted(&m.roleOrder, roles)

	for _, role := range m.roleOrder {
		if !namePattern.MatchString(role) {
			return nil, fmt.Errorf("invalid role name")
		}
		info, ok := asTable(roles[role])
		if !ok {
			return nil, fmt.Errorf("role %s must be a table", role)
		}
		if tier, ok := info["tier"].(string); !ok || (tier != "boot" && tier != "pop") {
			return nil, fmt.Errorf("role %s: tier must be boot or pop", role)
		}
		if skills, exists := info["skills"]; exists && !validNames(skills) {
			return nil, fmt.Errorf("role %s: invalid skills", role)
		}
		if _, exists := info["fallback_profiles"]; exists && version != 2 {
			return nil, fmt.Errorf("role %s: fallback_profiles needs schema_version 2", role)
		}
		if _, _, localRules, err := roleRules(role, info); err != nil {
			return nil, err
		} else if version == 1 {
			rules, err := mergeToolRules(role, m.SharedToolRules, localRules)
			if err != nil {
				return nil, err
			}
			perm, _ := info["permission"].(string)
			if err := checkAskPermission(role, rules, "claude-code", perm); err != nil {
				return nil, err
			}
		}
	}

	if version == 2 {
		if err := checkKeys(raw, set("schema_version", "defaults", "profiles", "roles", "adapters", "tool_rules"), "manifest"); err != nil {
			return nil, err
		}
		defaultsValue, exists := raw["defaults"]
		if !exists {
			m.Defaults = map[string]interface{}{}
		} else {
			m.Defaults, ok = asTable(defaultsValue)
			if !ok {
				return nil, fmt.Errorf("defaults must be a table")
			}
		}
		if err := checkKeys(m.Defaults, set("profile"), "defaults"); err != nil {
			return nil, err
		}
		profiles, ok := asTable(raw["profiles"])
		if !ok || len(profiles) == 0 {
			return nil, fmt.Errorf("profiles must be a non-empty table")
		}
		m.Profiles = profiles
		appendMissingSorted(&m.profileOrder, profiles)
		for _, name := range m.profileOrder {
			if !namePattern.MatchString(name) {
				return nil, fmt.Errorf("invalid profile name")
			}
			_, ok := asTable(profiles[name])
			if !ok {
				return nil, fmt.Errorf("profile %s must be a table", name)
			}
		}
		if adapters, exists := raw["adapters"]; exists {
			m.Adapters, _ = asTable(adapters)
		}
		for _, role := range m.roleOrder {
			if _, err := m.Resolve(role); err != nil {
				return nil, err
			}
			if err := m.checkFallbacks(role); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

// Resolve expands one role using the selected/default profile.
func (m *Manifest) Resolve(role string) (Profile, error) { return m.resolve(role, "") }

// ResolveWith expands one role on another profile (schema 2): the role's
// tier and skills, that profile's client, provider, model and options. It is
// what `agentbus swap` launches (v0.15 §6).
func (m *Manifest) ResolveWith(role, profile string) (Profile, error) {
	if profile == "" {
		return m.Resolve(role)
	}
	if m.SchemaVersion != 2 {
		return Profile{}, fmt.Errorf("--profile needs a schema_version 2 manifest")
	}
	return m.resolve(role, profile)
}

// MaxFallbacks bounds fallback_profiles: an ask question (six options at
// most) must hold them plus "Wait for the reset".
const MaxFallbacks = 3

// Fallbacks is the role's fallback_profiles, in order (schema 2; none in v1).
func (m *Manifest) Fallbacks(role string) []string {
	info, _ := asTable(m.Roles[role])
	names, _ := stringNames(info["fallback_profiles"])
	return names
}

// checkFallbacks (v0.15 §4): each fallback is a defined profile that resolves
// for the role, not the role's own profile, not on the same provider (it
// would be held too), at most MaxFallbacks, no repeats.
func (m *Manifest) checkFallbacks(role string) error {
	info, _ := asTable(m.Roles[role])
	raw, exists := info["fallback_profiles"]
	if !exists {
		return nil
	}
	names, ok := stringNames(raw)
	if !ok {
		return fmt.Errorf("role %s: fallback_profiles must be an array of profile names", role)
	}
	if len(names) > MaxFallbacks {
		return fmt.Errorf("role %s: at most %d fallback_profiles", role, MaxFallbacks)
	}
	own, err := m.Resolve(role)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return fmt.Errorf("role %s: fallback profile %s listed twice", role, name)
		}
		seen[name] = true
		if name == own.Profile {
			return fmt.Errorf("role %s: fallback profile %s is the role's own profile", role, name)
		}
		if _, ok := m.Profiles[name]; !ok {
			return fmt.Errorf("role %s: unknown fallback profile %s", role, name)
		}
		fb, err := m.resolve(role, name)
		if err != nil {
			return fmt.Errorf("role %s: fallback profile %s: %w", role, name, err)
		}
		if fb.Provider == own.Provider {
			return fmt.Errorf("role %s: fallback profile %s is on the same provider (%s): it would be held too", role, name, own.Provider)
		}
	}
	return nil
}

func (m *Manifest) resolve(role, override string) (Profile, error) {
	infoRaw, exists := m.Roles[role]
	if !exists {
		return Profile{}, fmt.Errorf("unknown role: %s", role)
	}
	info, ok := asTable(infoRaw)
	if !ok {
		return Profile{}, fmt.Errorf("role %s must be a table", role)
	}
	legacy := m.SchemaVersion == 1
	profileName := "legacy"
	profile := map[string]interface{}{}
	if legacy {
		profile["client"] = "claude-code"
		profile["provider"] = "anthropic"
		profile["model"] = info["model"]
		options := map[string]interface{}{}
		// The legacy Python resolver always includes this key, even when the
		// manifest omits it; the built-in Claude option validation rejects nil.
		options["permission"] = info["permission"]
		if v, ok := info["fallback"]; ok && truthy(v) {
			options["fallback"] = v
		}
		profile["options"] = options
	} else {
		if err := checkKeys(info, set("profile", "tier", "skills", "fallback_profiles", "pops", "approval", "tool_rules"), "role "+role+" (move execution fields into a profile)"); err != nil {
			return Profile{}, err
		}
		profileNameValue, ok := info["profile"]
		if !ok {
			profileNameValue = m.Defaults["profile"]
		}
		if override != "" {
			profileNameValue = override
		}
		var valid bool
		profileName, valid = profileNameValue.(string)
		rawProfile, profileExists := m.Profiles[profileName]
		if !valid || !profileExists {
			return Profile{}, fmt.Errorf("role %s: unknown or missing profile", role)
		}
		var profileIsTable bool
		profile, profileIsTable = asTable(rawProfile)
		if !profileIsTable {
			return Profile{}, fmt.Errorf("profile %s must be a table", profileName)
		}
		if err := checkKeys(profile, set("client", "provider", "model", "tier", "reasoning", "reasoning_levels", "options", "env_refs"), "profile "+profileName); err != nil {
			return Profile{}, err
		}
	}
	client, ok := checkedString(profile["client"], "client")
	if !ok {
		return Profile{}, fmt.Errorf("client must be a non-empty string without control characters")
	}
	provider, ok := checkedString(profile["provider"], "provider")
	if !ok {
		return Profile{}, fmt.Errorf("provider must be a non-empty string without control characters")
	}
	// v0.15.2 §10.3: a profile names a tier instead of a model; the launch
	// resolves it from the model table. Never both.
	tier := ""
	if v, exists := profile["tier"]; exists {
		t, ok := v.(string)
		if !ok || !models.ValidTier(t) {
			return Profile{}, fmt.Errorf("tier must be one of frontier, workhorse, fast, legacy")
		}
		if _, both := profile["model"]; both {
			return Profile{}, fmt.Errorf("profile %s: set model or tier, not both", profileName)
		}
		tier = t
	}
	model, ok := checkedString(profile["model"], "model")
	if tier != "" {
		model, ok = "", true // resolved at launch
	}
	if !ok {
		return Profile{}, fmt.Errorf("model must be a non-empty string without control characters")
	}
	if strings.HasPrefix(model, "-") {
		return Profile{}, fmt.Errorf("model must not start with '-'")
	}
	if !namePattern.MatchString(client) || !namePattern.MatchString(provider) {
		return Profile{}, fmt.Errorf("invalid client/provider identifier")
	}
	options, exists := profile["options"]
	optionMap := map[string]interface{}{}
	if exists {
		var ok bool
		optionMap, ok = asTable(options)
		if !ok {
			return Profile{}, fmt.Errorf("options must be a table")
		}
	}
	if isBuiltin(client) {
		for key, value := range optionMap {
			if _, ok := checkedString(value, client+" option "+key); !ok {
				return Profile{}, fmt.Errorf("%s option %s must be a non-empty string without control characters", client, key)
			}
		}
	}
	refs := map[string]string{}
	if value, exists := profile["env_refs"]; exists {
		refMap, ok := asTable(value)
		if !ok {
			return Profile{}, fmt.Errorf("env_refs must map environment variable names to source variable names")
		}
		for target, sourceValue := range refMap {
			source, ok := sourceValue.(string)
			if !envPattern.MatchString(target) || !ok || !envPattern.MatchString(source) {
				return Profile{}, fmt.Errorf("env_refs must map environment variable names to source variable names")
			}
			refs[target] = source
		}
	}
	for key := range refs {
		if envRefForbidden(key) {
			return Profile{}, fmt.Errorf("env_refs cannot override launcher identity or process controls")
		}
	}
	reasoningLevelsValue, exists := profile["reasoning_levels"]
	levels := []string{}
	if exists {
		var valid bool
		levels, valid = stringNames(reasoningLevelsValue)
		if !valid {
			return Profile{}, fmt.Errorf("reasoning_levels must be an array of level names")
		}
	}
	reasoning := ""
	if value, exists := profile["reasoning"]; exists {
		var valid bool
		reasoning, valid = checkedString(value, "reasoning")
		if !valid {
			return Profile{}, fmt.Errorf("reasoning must be a non-empty string without control characters")
		}
		if !contains(levels, reasoning) {
			return Profile{}, fmt.Errorf("reasoning requires reasoning_levels declaring support for this model/provider")
		}
	}
	resolved := Profile{
		Client: client, Provider: provider, Model: model, Tier: tier, Reasoning: reasoning,
		ReasoningLevels: levels, Options: map[string]string{}, OptionsRaw: cloneTable(optionMap), EnvRefs: refs,
		Profile: profileName, Role: role, Legacy: legacy, Manifest: m.manifestPath,
	}
	for key, value := range optionMap {
		if text, ok := value.(string); ok {
			resolved.Options[key] = text
		} else if isBuiltin(client) {
			// Builtin option types have already been rejected above.
			return Profile{}, fmt.Errorf("%s option %s must be a non-empty string without control characters", client, key)
		}
	}
	if rawSkills, exists := info["skills"]; exists {
		resolved.Skills, _ = stringNames(rawSkills)
	}
	var err error
	var localRules ToolRules
	if resolved.Pops, resolved.Approval, localRules, err = roleRules(role, info); err != nil {
		return Profile{}, err
	}
	if resolved.ToolRules, err = mergeToolRules(role, m.SharedToolRules, localRules); err != nil {
		return Profile{}, err
	}
	if err := checkAskPermission(role, resolved.ToolRules, client, resolved.Options["permission"]); err != nil {
		return Profile{}, err
	}
	if client == "claude-code" {
		if err := checkKeys(optionMap, set("permission", "fallback"), "Claude options"); err != nil {
			return Profile{}, err
		}
		if v, exists := optionMap["permission"]; exists && v != "default" && v != "acceptEdits" && v != "auto" && v != "bypassPermissions" && v != "manual" && v != "dontAsk" && v != "plan" {
			return Profile{}, fmt.Errorf("unsupported Claude permission")
		}
		if v, exists := optionMap["fallback"]; exists {
			if _, ok := checkedString(v, "fallback"); !ok {
				return Profile{}, fmt.Errorf("fallback must be a non-empty string without control characters")
			}
		}
		if reasoning != "" && !contains([]string{"low", "medium", "high", "xhigh", "max"}, reasoning) {
			return Profile{}, fmt.Errorf("unsupported Claude reasoning level")
		}
	} else if client == "codex" {
		if err := checkKeys(optionMap, set("sandbox", "approval"), "Codex options"); err != nil {
			return Profile{}, err
		}
		if v, exists := optionMap["sandbox"]; exists && v != "read-only" && v != "workspace-write" && v != "danger-full-access" {
			return Profile{}, fmt.Errorf("unsupported Codex sandbox")
		}
		if v, exists := optionMap["approval"]; exists && v != "on-request" && v != "never" {
			return Profile{}, fmt.Errorf("unsupported Codex approval")
		}
		if reasoning != "" && !contains([]string{"minimal", "low", "medium", "high", "xhigh", "max", "ultra", "none"}, reasoning) {
			return Profile{}, fmt.Errorf("unsupported Codex reasoning level")
		}
	} else if client == "kimi-cli" {
		if err := checkKeys(optionMap, set("permission"), "Kimi options"); err != nil {
			return Profile{}, err
		}
		if v, exists := optionMap["permission"]; exists && v != "default" && v != "yolo" && v != "auto" && v != "plan" {
			return Profile{}, fmt.Errorf("unsupported Kimi permission")
		}
		if reasoning != "" {
			return Profile{}, fmt.Errorf("kimi-cli adapter has no verified reasoning override; omit reasoning and configure the native model alias")
		}
	} else if client == "grok" { // Grok Build: the values its --help lists (grok 1.0.67)
		if err := checkKeys(optionMap, set("permission"), "Grok options"); err != nil {
			return Profile{}, err
		}
		if v, exists := optionMap["permission"]; exists && v != "default" && v != "acceptEdits" && v != "auto" && v != "dontAsk" && v != "bypassPermissions" && v != "plan" {
			return Profile{}, fmt.Errorf("unsupported Grok permission")
		}
		if reasoning != "" {
			return Profile{}, fmt.Errorf("grok adapter has no verified reasoning levels (--help lists none); omit reasoning")
		}
	} else {
		adapters, exists := m.rawAdapters()
		if !exists || adapters[client] == nil {
			return Profile{}, fmt.Errorf("unknown client %s: register an adapter", client)
		}
		adapter, ok := asTable(adapters[client])
		if !ok {
			return Profile{}, fmt.Errorf("adapter %s must be a table", client)
		}
		if err := checkKeys(adapter, set("executable"), "adapter "+client); err != nil {
			return Profile{}, err
		}
		executable, ok := checkedString(adapter["executable"], "executable")
		if !ok {
			return Profile{}, fmt.Errorf("executable must be a non-empty string without control characters")
		}
		adapterPath := executable
		if !filepath.IsAbs(adapterPath) {
			adapterPath = filepath.Join(filepath.Dir(m.manifestPath), adapterPath)
		}
		adapterPath = filepath.Clean(adapterPath)
		if canonical, err := filepath.EvalSymlinks(adapterPath); err == nil {
			adapterPath = canonical
		}
		resolved.Adapter = adapterPath
	}
	return resolved, nil
}

// RolesByTier returns roles in their original manifest order.
func (m *Manifest) RolesByTier(tier string) []string {
	roles := make([]string, 0)
	for _, name := range m.roleOrder {
		info, _ := asTable(m.Roles[name])
		if info["tier"] == tier {
			roles = append(roles, name)
		}
	}
	return roles
}

// Field follows the launcher field action: role fields take precedence over
// resolved profile fields, which take precedence over options.
func (m *Manifest) Field(role, name string) (values []string, ok bool, err error) {
	infoRaw, exists := m.Roles[role]
	if !exists {
		return nil, false, nil
	}
	info, ok := asTable(infoRaw)
	if !ok {
		return nil, false, fmt.Errorf("role %s must be a table", role)
	}
	profile, err := m.Resolve(role)
	if err != nil {
		return nil, false, err
	}
	var value interface{}
	if v, exists := info[name]; exists {
		value, ok = v, true
	} else if v, exists := profileField(profile, name); exists {
		value, ok = v, true
	} else if v, exists := profile.OptionsRaw[name]; exists {
		value, ok = v, true
	}
	if !ok || value == nil {
		return nil, false, nil
	}
	if list, isList := fieldStrings(value); isList {
		return list, true, nil
	}
	return []string{pythonString(value)}, true, nil
}

func profileField(p Profile, name string) (interface{}, bool) {
	switch name {
	case "client":
		return p.Client, true
	case "provider":
		return p.Provider, true
	case "model":
		return p.Model, true
	case "reasoning":
		if p.Reasoning != "" {
			return p.Reasoning, true
		}
	case "reasoning_levels":
		return p.ReasoningLevels, true
	case "profile":
		return p.Profile, true
	case "role":
		return p.Role, true
	case "legacy":
		return p.Legacy, true
	case "skills":
		return p.Skills, true
	case "manifest":
		return p.Manifest, true
	case "adapter":
		if p.Adapter != "" {
			return p.Adapter, true
		}
	case "options":
		return p.OptionsRaw, true
	case "env_refs":
		return p.EnvRefs, true
	}
	return nil, false
}

func (m *Manifest) rawAdapters() (map[string]interface{}, bool) { return m.Adapters, m.Adapters != nil }

func asTable(value interface{}) (map[string]interface{}, bool) {
	if table, ok := value.(map[string]interface{}); ok {
		return table, true
	}
	return nil, false
}

func checkedString(value interface{}, _ string) (string, bool) {
	s, ok := value.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", false
	}
	for _, r := range s {
		if r < 32 {
			return "", false
		}
	}
	return s, true
}

func validNames(value interface{}) bool {
	_, ok := stringNames(value)
	return ok
}

func stringNames(value interface{}) ([]string, bool) {
	var raw []interface{}
	switch values := value.(type) {
	case []interface{}:
		raw = values
	case []string:
		return append([]string(nil), values...), allNames(values)
	default:
		return nil, false
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		s, ok := v.(string)
		if !ok || !namePattern.MatchString(s) {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

func allNames(values []string) bool {
	for _, value := range values {
		if !namePattern.MatchString(value) {
			return false
		}
	}
	return true
}

func fieldStrings(value interface{}) ([]string, bool) {
	switch v := value.(type) {
	case []interface{}:
		out := make([]string, len(v))
		for i, item := range v {
			out[i] = pythonString(item)
		}
		return out, true
	case []string:
		return append([]string(nil), v...), true
	default:
		return nil, false
	}
}

func cloneTable(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func checkKeys(table map[string]interface{}, allowed map[string]bool, label string) error {
	if table == nil {
		return fmt.Errorf("%s must be a table", label)
	}
	unknown := make([]string, 0)
	for key := range table {
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown fields in %s: %s", label, strings.Join(unknown, ", "))
	}
	return nil
}

func set(keys ...string) map[string]bool {
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[key] = true
	}
	return out
}

func appendUnique(list []string, item string) []string {
	for _, existing := range list {
		if existing == item {
			return list
		}
	}
	return append(list, item)
}

func appendMissingSorted(order *[]string, table map[string]interface{}) {
	missing := make([]string, 0)
	for name := range table {
		found := false
		for _, existing := range *order {
			if existing == name {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	*order = append(*order, missing...)
}

func isBuiltin(client string) bool {
	return client == "claude-code" || client == "codex" || client == "kimi-cli" || client == "grok"
}

func contains(values []string, sought string) bool {
	for _, value := range values {
		if value == sought {
			return true
		}
	}
	return false
}

func truthy(value interface{}) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case bool:
		return v
	case int64:
		return v != 0
	case float64:
		return v != 0
	case []interface{}:
		return len(v) != 0
	case map[string]interface{}:
		return len(v) != 0
	default:
		return true
	}
}

func pythonString(value interface{}) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "True"
		}
		return "False"
	case []interface{}:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = pythonString(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]interface{}:
		return fmt.Sprint(v)
	default:
		return fmt.Sprint(value)
	}
}

// RolesUsing is every role whose skills include one of skills, in manifest
// order: the agents to tell when those skills change (a running agent keeps
// the copy it loaded at boot until it invokes the skill again).
func (m *Manifest) RolesUsing(skills []string) []string {
	var out []string
	for _, role := range m.roleOrder {
		p, err := m.Resolve(role)
		if err != nil {
			continue
		}
		for _, s := range p.Skills {
			if contains(skills, s) {
				out = append(out, role)
				break
			}
		}
	}
	return out
}
