package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"iter"
	"os"
	pathpkg "path"
	"regexp"
	"runtime"
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

	ServiceTmpl   = template.Must(template.New("service").Parse(serviceStr))
	UserTmpl      = template.Must(template.New("user").Parse(userStr))
	SetupTmpl     = template.Must(template.New("setup").Parse(setupStr))
	UninstallTmpl = template.Must(template.New("uninstall").Parse(uninstallStr))
	LogrotateTmpl = template.Must(template.New("logrotate").Parse(logrotateStr))

	managedKeys = initManagedKeys()

	serviceNameRgx = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,30}$`)
	safePathRgx    = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
	cpuQuotaRgx    = regexp.MustCompile(`^[1-9][0-9]*(?:\.[0-9]+)?%$`)
	memoryMaxRgx   = regexp.MustCompile(`^[1-9][0-9]*(?:\.[0-9]+)?[KMGTPE]?$`)
	directiveRgx   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	configFileRgx  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

	preservedCustomKeys = map[string]bool{
		"Environment": true,
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
	WritableFiles   bool   `yaml:"writable_files"`
	WritableConfig  bool   `yaml:"writable_config"`
	ConfigFile      string `yaml:"config_file,omitempty"`
	RuntimeDir      bool   `yaml:"runtime_dir"`
	Devices         bool   `yaml:"devices"`
	FullDevices     bool   `yaml:"full_devices"`
	Subprocess      bool   `yaml:"subprocess"`
	SeparateLogDir  bool   `yaml:"separate_log_dir"`

	// Advanced security
	LocalhostOnly bool `yaml:"localhost_only"`
	PrivateUsers  bool `yaml:"private_users"`

	// Resource limits (empty = no limit)
	CPUQuota  string `yaml:"cpu_quota,omitempty"`
	MemoryMax string `yaml:"memory_max,omitempty"`

	// Environment
	EnvFile string `yaml:"env_file,omitempty"`

	// Internal (not persisted)
	After    string              `yaml:"-"`
	Requires string              `yaml:"-"`
	Defaults map[string]string   `yaml:"-"`
	Custom   map[string][]string `yaml:"-"`
}

func initManagedKeys() map[string]bool {
	keys := make(map[string]bool)

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

	if err := yaml.UnmarshalWithOptions(data, &cfg, yaml.Strict()); err != nil {
		return nil, err
	}

	cfg.Defaults = defaultLimits()
	cfg.Custom = make(map[string][]string)
	cfg.UpdateLabel()

	return &cfg, nil
}

func (cfg *ServiceConfig) Normalize() {
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

	if !validServicePath(cfg.Path) {
		return fmt.Errorf("service path must be a clean absolute path below /opt, /srv, /var/lib, or /usr/local/lib")
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

func (cfg *ServiceConfig) PreserveCustom(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}

		return err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))

	var (
		inUnit       bool
		inService    bool
		seenAfter    bool
		seenRequires bool
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inUnit = line == "[Unit]"
			inService = line == "[Service]"

			continue
		}

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) < 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if !directiveRgx.MatchString(key) || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("invalid directive in %s", path)
		}

		if inUnit {
			switch key {
			case "After":
				value = removeManagedTargets(value)
				if value == "" {
					continue
				}

				if seenAfter {
					cfg.After += " " + value
				} else {
					cfg.After = value

					seenAfter = true
				}
			case "Requires":
				value = removeManagedTargets(value)
				if value == "" {
					continue
				}

				if seenRequires {
					cfg.Requires += " " + value
				} else {
					cfg.Requires = value

					seenRequires = true
				}
			}
		} else if inService {
			if !managedKeys[key] && cfg.Defaults[key] != value {
				if !preservedCustomKeys[key] {
					return fmt.Errorf("refusing to preserve unsupported directive %s", key)
				}

				cfg.Custom[key] = append(cfg.Custom[key], value)

				delete(cfg.Defaults, key)
			}
		}
	}

	return scanner.Err()
}

func (cfg *ServiceConfig) ApplyDefaultAfter() {
	var afters, requires []string

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
	if !cfg.Devices {
		return
	}

	if cfg.FullDevices {
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
	keys := make([]string, 0, len(cfg.Custom))

	for k := range cfg.Custom {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var lines []string

	for _, k := range keys {
		for _, v := range cfg.Custom[k] {
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

	if err := tmpl.Execute(&data, cfg); err != nil {
		return err
	}

	return writeFileAtomic(path, data.Bytes(), 0644)
}

func validServicePath(value string) bool {
	if !validAbsolutePath(value) {
		return false
	}

	for _, root := range []string{"/opt", "/srv", "/var/lib", "/usr/local/lib"} {
		if strings.HasPrefix(value, root+"/") {
			return true
		}
	}

	return false
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

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(pathpkg.Dir(path), ".mksvc-*")
	if err != nil {
		return err
	}

	tempPath := file.Name()

	defer os.Remove(tempPath)

	err = file.Chmod(mode)
	if err != nil {
		file.Close()

		return err
	}

	_, err = file.Write(data)
	if err != nil {
		file.Close()

		return err
	}

	err = file.Sync()
	if err != nil {
		file.Close()

		return err
	}

	err = file.Close()
	if err != nil {
		return err
	}

	err = os.Rename(tempPath, path)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}

	info, statErr := os.Lstat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}

	if statErr == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular file %s", path)
		}

		if err := os.Remove(path); err != nil {
			return err
		}
	}

	return os.Rename(tempPath, path)
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
