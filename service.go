package main

import (
	"bufio"
	"bytes"
	_ "embed"
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

	ServiceTmpl = template.Must(template.New("service").Parse(ServiceStr))
	UserTmpl    = template.Must(template.New("user").Parse(UserStr))
	SetupTmpl   = template.Must(template.New("setup").Parse(SetupStr))

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

	NeedsNetwork       bool
	NeedsListening     bool
	NeedsExecMemory    bool
	NeedsWritableFiles bool
	NeedsRuntimeDir    bool
	NeedsDevices       bool
	NeedsSubprocess    bool

	Defaults map[string]string
	Custom   map[string]string
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

		After:    "network-online.target",
		Requires: "",

		NeedsNetwork:       true,
		NeedsListening:     true,
		NeedsExecMemory:    false,
		NeedsWritableFiles: true,
		NeedsRuntimeDir:    false,
		NeedsDevices:       false,
		NeedsSubprocess:    false,

		Defaults: map[string]string{
			"LimitNOFILE":     "65536",
			"LimitNPROC":      "4096",
			"LimitCORE":       "0",
			"TimeoutStartSec": "300",
			"TimeoutStopSec":  "300",
		},
		Custom: make(map[string]string),
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
				cfg.Custom[key] = value

				delete(cfg.Defaults, key)
			}
		}
	}

	return scanner.Err()
}

func (cfg *ServiceConfig) FormatAttributes(source map[string]string) string {
	lines := make([]string, 0, len(source))

	for key, value := range source {
		lines = append(lines, key+"="+value)
	}

	sort.Strings(lines)

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
