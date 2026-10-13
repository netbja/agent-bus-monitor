// Package rolecfg loads and validates provider-neutral agent role manifests.
package rolecfg

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	envPattern  = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
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
	SchemaVersion int
	Defaults      map[string]interface{}
	Profiles      map[string]interface{}
	Roles         map[string]interface{}
	Adapters      map[string]interface{}
	manifestPath  string
	roleOrder     []string
	profileOrder  []string
}

// Profile is the resolved, client-ready configuration for one role.
type Profile struct {
	Client          string                 `json:"client"`
	Provider        string                 `json:"provider"`
	Model           string                 `json:"model"`
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
	}

	if version == 2 {
		if err := checkKeys(raw, set("schema_version", "defaults", "profiles", "roles", "adapters"), "manifest"); err != nil {
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
		}
	}
	return m, nil
}

// Resolve expands one role using the selected/default profile.
func (m *Manifest) Resolve(role string) (Profile, error) {
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
		if err := checkKeys(info, set("profile", "tier", "skills"), "role "+role+" (move execution fields into a profile)"); err != nil {
			return Profile{}, err
		}
		profileNameValue, ok := info["profile"]
		if !ok {
			profileNameValue = m.Defaults["profile"]
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
		if err := checkKeys(profile, set("client", "provider", "model", "reasoning", "reasoning_levels", "options", "env_refs"), "profile "+profileName); err != nil {
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
	model, ok := checkedString(profile["model"], "model")
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
		Client: client, Provider: provider, Model: model, Reasoning: reasoning,
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
	return client == "claude-code" || client == "codex" || client == "kimi-cli"
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
