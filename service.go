package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"iter"
	"os"
	pathpkg "path"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"github.com/goccy/go-yaml"
)

var (
	//go:embed templates/service.tmpl
	serviceStr string

	//go:embed templates/user.tmpl
	userStr string

	//go:embed templates/setup.tmpl
	setupStr string

	//go:embed templates/uninstall.tmpl
	uninstallStr string

	//go:embed templates/logrotate.tmpl
	logrotateStr string

	//go:embed templates/logging.tmpl
	loggingStr string

	ServiceTmpl   = template.Must(template.New("service").Parse(serviceStr))
	UserTmpl      = template.Must(template.New("user").Parse(userStr))
	SetupTmpl     = template.Must(template.New("setup").Parse(setupStr + loggingStr))
	UninstallTmpl = template.Must(template.New("uninstall").Parse(uninstallStr))
	LogrotateTmpl = template.Must(template.New("logrotate").Parse(logrotateStr))

	managedKeys = initManagedKeys()

	serviceNameRgx = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)
	safePathRgx    = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
	cpuQuotaRgx    = regexp.MustCompile(`^(?:[1-9][0-9]*(?:\.[0-9]+)?|0\.[0-9]*[1-9][0-9]*)%$`)
	memoryMaxRgx   = regexp.MustCompile(`^[1-9][0-9]*(?:\.[0-9]+)?[KMGTPE]?$`)
	directiveRgx   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	configFileRgx  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

	preservedCustomKeys = map[string]bool{
		"Environment":     true,
		"TimeoutStartSec": true,
		"TimeoutStopSec":  true,
		"DeviceAllow":     true,
	}
)

type ServiceConfig struct {
	Name  string `yaml:"name"`
	Path  string `yaml:"path"`
	Label string `yaml:"-"`

	// Core options
	Network         bool   `yaml:"network"`
	Listening       bool   `yaml:"listening"`
	PrivilegedPorts bool   `yaml:"privileged_ports"`
	ExecMemory      bool   `yaml:"exec_memory"`
	Memfd           bool   `yaml:"memfd"`
	WritableFiles   bool   `yaml:"writable_files"`
	WritableConfig  bool   `yaml:"writable_config"`
	ConfigFile      string `yaml:"config_file,omitempty"`
	RuntimeDir      bool   `yaml:"runtime_dir"`
	Devices         bool   `yaml:"devices"`
	FullDevices     bool   `yaml:"full_devices"`
	Subprocess      bool   `yaml:"subprocess"`
	SeparateLogDir  bool   `yaml:"separate_log_dir"`
	Journald        bool   `yaml:"journald"`

	// Advanced security
	LocalhostOnly bool `yaml:"localhost_only"`
	PrivateUsers  bool `yaml:"private_users"`

	// Resource limits (empty = no limit)
	CPUQuota  string `yaml:"cpu_quota,omitempty"`
	MemoryMax string `yaml:"memory_max,omitempty"`

	// Environment
	EnvFile string `yaml:"env_file,omitempty"`

	// Internal (not persisted)
	After             string              `yaml:"-"`
	Requires          string              `yaml:"-"`
	Defaults          map[string]string   `yaml:"-"`
	Custom            map[string][]string `yaml:"-"`
	UnitCustom        map[string][]string `yaml:"-"`
	InstallCustom     map[string][]string `yaml:"-"`
	HasInstallSection bool                `yaml:"-"`
}

func initManagedKeys() map[string]bool {
	keys := make(map[string]bool)
	// Recognize the obsolete setting so existing generated units migrate cleanly.
	keys["UtmpMode"] = true
	keys["InaccessiblePaths"] = true

	start := strings.Index(serviceStr, "[Service]")
	if start == -1 {
		start = 0
	}

	end := strings.Index(serviceStr, "[Install]")
	if end == -1 {
		end = len(serviceStr)
	}

	re := regexp.MustCompile(`(?m)(?:^|})(\w+)=`)

	for _, m := range re.FindAllStringSubmatch(serviceStr[start:end], -1) {
		if len(m) > 1 {
			keys[m[1]] = true
		}
	}

	return keys
}

func NewServiceConfig(name, path string) *ServiceConfig {
	cleanName := cleanServiceName(name)

	cfg := &ServiceConfig{
		Name: cleanName,
		Path: path,

		Network:         false,
		Listening:       false,
		PrivilegedPorts: false,
		ExecMemory:      false,
		WritableFiles:   false,
		WritableConfig:  false,
		RuntimeDir:      false,
		Devices:         false,
		FullDevices:     false,
		Subprocess:      false,
		SeparateLogDir:  true,

		LocalhostOnly: false,
		PrivateUsers:  false,

		CPUQuota:  "",
		MemoryMax: "",

		Defaults: defaultLimits(),
		Custom:   make(map[string][]string),
	}

	cfg.UpdateLabel()

	return cfg
}

func LoadConfig(path string) (*ServiceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg ServiceConfig

	err = yaml.UnmarshalWithOptions(data, &cfg, yaml.Strict())
	if err != nil {
		return nil, err
	}

	cfg.Defaults = defaultLimits()
	cfg.Custom = make(map[string][]string)
	cfg.UpdateLabel()

	return &cfg, nil
}

func (cfg *ServiceConfig) Normalize() {
	if cfg.WritableConfig && cfg.ConfigFile == "" {
		cfg.ConfigFile = "config.yml"
	}

	if !cfg.WritableConfig {
		cfg.ConfigFile = ""
	}

	if !cfg.Network {
		cfg.Listening = false
		cfg.PrivilegedPorts = false
		cfg.LocalhostOnly = false
	}

	if !cfg.Listening {
		cfg.PrivilegedPorts = false
	}

	if !cfg.Devices {
		cfg.FullDevices = false
	}
}

func (cfg *ServiceConfig) Validate() error {
	if !serviceNameRgx.MatchString(cfg.Name) || cfg.Name == "root" || cfg.Name == "nobody" {
		return fmt.Errorf("invalid service name %q", cfg.Name)
	}

	if !validAbsolutePath(cfg.Path) {
		return fmt.Errorf("invalid service root path %q", cfg.Path)
	}

	if cfg.EnvFile != "" && !validAbsolutePath(cfg.EnvFile) {
		return fmt.Errorf("invalid environment file path %q", cfg.EnvFile)
	}

	if cfg.CPUQuota != "" && !cpuQuotaRgx.MatchString(cfg.CPUQuota) {
		return fmt.Errorf("invalid CPU quota %q", cfg.CPUQuota)
	}

	if cfg.MemoryMax != "" && !memoryMaxRgx.MatchString(cfg.MemoryMax) {
		return fmt.Errorf("invalid memory limit %q", cfg.MemoryMax)
	}

	if cfg.WritableConfig {
		if !configFileRgx.MatchString(cfg.ConfigFile) || cfg.ConfigFile == "." || cfg.ConfigFile == ".." {
			return fmt.Errorf("invalid writable config filename %q", cfg.ConfigFile)
		}

		reserved := map[string]bool{
			cfg.Name:          true,
			cfg.Name + ".log": true,
			"conf":            true,
			"data":            true,
			"logs":            true,
		}

		if reserved[cfg.ConfigFile] {
			return fmt.Errorf("writable config filename %q conflicts with a managed path", cfg.ConfigFile)
		}
	} else if cfg.ConfigFile != "" {
		return fmt.Errorf("config_file requires writable_config")
	}

	return nil
}

func (cfg *ServiceConfig) SaveConfig(path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return writeFileAtomic(path, data, 0600)
}

func (cfg *ServiceConfig) UpdateLabel() {
	words := strings.FieldsFunc(cfg.Name, func(r rune) bool {
		return unicode.IsSpace(r) || r == '_' || r == '-'
	})

	for i, word := range words {
		runes := []rune(word)

		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])

			words[i] = string(runes)
		}
	}

	cfg.Label = strings.Join(words, " ")
}

func (cfg *ServiceConfig) ApplyDefaultAfter() {
	var (
		afters   []string
		requires []string
	)

	if cfg.Network {
		target := "network.target"

		if cfg.Listening {
			target = "network-online.target"
		}

		afters = append(afters, target)
		requires = append(requires, target)
	} else {
		afters = append(afters, "local-fs.target")
	}

	cfg.After = strings.TrimSpace(prependUnique(strings.FieldsSeq(cfg.After), afters))
	cfg.Requires = strings.TrimSpace(prependUnique(strings.FieldsSeq(cfg.Requires), requires))
}

func (cfg *ServiceConfig) ApplyDeviceDefaults() {
	if !cfg.Devices || cfg.FullDevices {
		delete(cfg.Custom, "DeviceAllow")

		return
	}

	if _, exists := cfg.Custom["DeviceAllow"]; !exists {
		cfg.Custom["DeviceAllow"] = []string{
			"char-usb rwm",
			"char-tty rwm",
		}
	}
}

func (cfg *ServiceConfig) FormatDefaults() string {
	return formatMap(cfg.Defaults)
}

func (cfg *ServiceConfig) FormatCustom() string {
	return formatDirectives(cfg.Custom)
}

func (cfg *ServiceConfig) FormatUnitCustom() string {
	return formatDirectives(cfg.UnitCustom)
}

func (cfg *ServiceConfig) FormatInstallCustom() string {
	return formatDirectives(cfg.InstallCustom)
}

func formatDirectives(values map[string][]string) string {
	keys := make([]string, 0, len(values))

	for k := range values {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var lines []string

	for _, k := range keys {
		for _, v := range values[k] {
			lines = append(lines, k+"="+v)
		}
	}

	return strings.Join(lines, "\n")
}

func (cfg *ServiceConfig) CanHavePrivateUsers() (bool, string) {
	if cfg.PrivilegedPorts {
		return false, "disabled because privileged ports require CAP_NET_BIND_SERVICE"
	}

	if cfg.Devices {
		return false, "disabled because device access + supplementary groups are enabled"
	}

	if _, ok := cfg.Custom["SupplementaryGroups"]; ok {
		return false, "disabled because SupplementaryGroups is set"
	}

	return true, ""
}

func (cfg *ServiceConfig) WriteTemplate(path string, tmpl *template.Template) error {
	path = strings.Replace(path, "{name}", cfg.Name, 1)

	var data bytes.Buffer

	err := tmpl.Execute(&data, cfg)
	if err != nil {
		return err
	}

	return writeFileAtomic(path, data.Bytes(), 0644)
}

func validAbsolutePath(value string) bool {
	return safePathRgx.MatchString(value) && pathpkg.IsAbs(value) && pathpkg.Clean(value) == value
}

func removeManagedTargets(value string) string {
	managed := map[string]bool{
		"local-fs.target":       true,
		"network.target":        true,
		"network-online.target": true,
	}

	values := strings.Fields(value)
	kept := values[:0]

	for _, item := range values {
		if !managed[item] {
			kept = append(kept, item)
		}
	}

	return strings.Join(kept, " ")
}

func defaultLimits() map[string]string {
	return map[string]string{
		"LimitNOFILE":     "65536",
		"LimitNPROC":      "4096",
		"LimitCORE":       "0",
		"TimeoutStartSec": "300",
		"TimeoutStopSec":  "300",
	}
}

func formatMap(m map[string]string) string {
	lines := make([]string, 0, len(m))

	for k, v := range m {
		lines = append(lines, k+"="+v)
	}

	sort.Strings(lines)

	return strings.Join(lines, "\n")
}

func prependUnique(existing iter.Seq[string], defaults []string) string {
	found := make(map[string]bool)

	var result []string

	for _, str := range defaults {
		if !found[str] {
			found[str] = true

			result = append(result, str)
		}
	}

	for str := range existing {
		if !found[str] && str != "" {
			found[str] = true

			result = append(result, str)
		}
	}

	return strings.Join(result, " ")
}

func cleanServiceName(name string) string {
	name = strings.ToLower(name)

	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, " ", "_")

	reg := regexp.MustCompile(`[^a-z0-9_-]`)
	name = reg.ReplaceAllString(name, "")

	if len(name) > 0 && unicode.IsDigit(rune(name[0])) {
		name = "svc_" + name
	}

	if name == "" {
		name = "service"
	}

	return name
}
