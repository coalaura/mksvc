package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"iter"
	"os"
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

	ServiceTmpl   = template.Must(template.New("service").Parse(serviceStr))
	UserTmpl      = template.Must(template.New("user").Parse(userStr))
	SetupTmpl     = template.Must(template.New("setup").Parse(setupStr))
	UninstallTmpl = template.Must(template.New("uninstall").Parse(uninstallStr))
	LogrotateTmpl = template.Must(template.New("logrotate").Parse(logrotateStr))

	managedKeys = initManagedKeys()
)

type ServiceConfig struct {
	Name  string `yaml:"name"`
	Path  string `yaml:"path"`
	Label string `yaml:"-"`

	Network         bool `yaml:"network"`
	Listening       bool `yaml:"listening"`
	PrivilegedPorts bool `yaml:"privileged_ports"`
	ExecMemory      bool `yaml:"exec_memory"`
	WritableFiles   bool `yaml:"writable_files"`
	RuntimeDir      bool `yaml:"runtime_dir"`
	Devices         bool `yaml:"devices"`
	FullDevices     bool `yaml:"full_devices"`
	Subprocess      bool `yaml:"subprocess"`
	SeparateLogDir  bool `yaml:"separate_log_dir"`

	EnvFile string `yaml:"env_file,omitempty"`

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

		Network:         true,
		Listening:       true,
		PrivilegedPorts: false,
		ExecMemory:      false,
		WritableFiles:   false,
		RuntimeDir:      false,
		Devices:         false,
		FullDevices:     false,
		Subprocess:      false,
		SeparateLogDir:  true,

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

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	cfg.Defaults = defaultLimits()
	cfg.Custom = make(map[string][]string)
	cfg.UpdateLabel()

	return &cfg, nil
}

func (cfg *ServiceConfig) SaveConfig(path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
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

		if inUnit {
			switch key {
			case "After":
				if seenAfter {
					cfg.After += " " + value
				} else {
					cfg.After = value

					seenAfter = true
				}
			case "Requires":
				if seenRequires {
					cfg.Requires += " " + value
				} else {
					cfg.Requires = value

					seenRequires = true
				}
			}
		} else if inService {
			if !managedKeys[key] && cfg.Defaults[key] != value {
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

	if _, exists := cfg.Custom["SupplementaryGroups"]; !exists {
		cfg.Custom["SupplementaryGroups"] = []string{"dialout", "plugdev"}
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

func (cfg *ServiceConfig) WriteTemplate(path string, tmpl *template.Template) error {
	path = strings.Replace(path, "{name}", cfg.Name, 1)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	defer file.Close()

	return tmpl.Execute(file, cfg)
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
