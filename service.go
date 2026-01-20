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
)

var (
	//go:embed templates/service.tmpl
	ServiceStr string

	//go:embed templates/user.tmpl
	UserStr string

	//go:embed templates/setup.tmpl
	SetupStr string

	//go:embed templates/logrotate.tmpl
	LogrotateStr string

	ServiceTmpl   = template.Must(template.New("service").Parse(ServiceStr))
	UserTmpl      = template.Must(template.New("user").Parse(UserStr))
	SetupTmpl     = template.Must(template.New("setup").Parse(SetupStr))
	LogrotateTmpl = template.Must(template.New("logrotate").Parse(LogrotateStr))

	managed = make(map[string]bool)
)

func init() {
	start := strings.Index(ServiceStr, "[Service]")
	if start == -1 {
		start = 0
	}

	end := strings.Index(ServiceStr, "[Install]")
	if end == -1 {
		end = len(ServiceStr)
	}

	block := ServiceStr[start:end]

	re := regexp.MustCompile(`(?m)(?:^|})(\w+)=`)
	matches := re.FindAllStringSubmatch(block, -1)

	for _, m := range matches {
		if len(m) > 1 {
			managed[m[1]] = true
		}
	}
}

type ServiceConfig struct {
	Name  string
	Label string
	Path  string

	After    string
	Requires string

	NeedsNetwork         bool
	NeedsListening       bool
	NeedsPrivilegedPorts bool
	NeedsExecMemory      bool
	NeedsWritableFiles   bool
	NeedsRuntimeDir      bool
	NeedsDevices         bool
	FullDevices          bool
	NeedsSubprocess      bool
	SeparateLogDir       bool

	Defaults map[string]string
	Custom   map[string][]string
}

func NewServiceConfig(name, path string) *ServiceConfig {
	name = strings.ToLower(name)

	words := strings.FieldsFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || r == '_' || r == '-'
	})

	for i, word := range words {
		runes := []rune(word)

		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
			words[i] = string(runes)
		}
	}

	return &ServiceConfig{
		Name:  name,
		Label: strings.Join(words, " "),
		Path:  path,

		After:    "",
		Requires: "",

		NeedsNetwork:         true,
		NeedsListening:       true,
		NeedsPrivilegedPorts: false,
		NeedsExecMemory:      false,
		NeedsWritableFiles:   false,
		NeedsRuntimeDir:      false,
		NeedsDevices:         false,
		FullDevices:          false,
		NeedsSubprocess:      false,
		SeparateLogDir:       true,

		Defaults: map[string]string{
			"LimitNOFILE":     "65536",
			"LimitNPROC":      "4096",
			"LimitCORE":       "0",
			"TimeoutStartSec": "300",
			"TimeoutStopSec":  "300",
		},
		Custom: make(map[string][]string),
	}
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
			inUnit = (line == "[Unit]")
			inService = (line == "[Service]")

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
				if !seenAfter {
					cfg.After = value

					seenAfter = true
				} else {
					cfg.After += " " + value
				}
			case "Requires":
				if !seenRequires {
					cfg.Requires = value

					seenRequires = true
				} else {
					cfg.Requires += " " + value
				}
			}
		} else if inService {
			if !managed[key] && cfg.Defaults[key] != value {
				cfg.Custom[key] = append(cfg.Custom[key], value)

				delete(cfg.Defaults, key)
			}
		}
	}

	return scanner.Err()
}

func (cfg *ServiceConfig) ApplyDefaultAfter() {
	var (
		afters   []string
		requires []string
	)

	if cfg.NeedsNetwork {
		target := "network.target"

		if cfg.NeedsListening {
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
	if !cfg.NeedsDevices {
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
	lines := make([]string, 0, len(cfg.Defaults))

	for key, value := range cfg.Defaults {
		lines = append(lines, key+"="+value)
	}

	sort.Strings(lines)

	return strings.Join(lines, "\n")
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

func (cfg *ServiceConfig) WriteService(path string, tmpl *template.Template) error {
	path = strings.Replace(path, "{name}", cfg.Name, 1)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	defer file.Close()

	return tmpl.Execute(file, cfg)
}

func prependUnique(existing iter.Seq[string], defaults []string) string {
	var result []string

	found := make(map[string]bool)

	for _, str := range defaults {
		if found[str] {
			continue
		}

		found[str] = true

		result = append(result, str)
	}

	for str := range existing {
		if found[str] || str == "" {
			continue
		}

		found[str] = true

		result = append(result, str)
	}

	return strings.Join(result, " ")
}
