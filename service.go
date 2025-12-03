package main

import (
	_ "embed"
	"os"
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
)

type ServiceConfig struct {
	Name  string
	Label string
	Path  string

	NeedsNetwork       bool
	NeedsListening     bool
	NeedsExecMemory    bool
	NeedsWritableFiles bool
	NeedsDevices       bool
	NeedsSubprocess    bool
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

		// Defaults for most generic services
		NeedsNetwork:       true,
		NeedsListening:     true,
		NeedsExecMemory:    false,
		NeedsWritableFiles: true,
		NeedsDevices:       false,
		NeedsSubprocess:    false,
	}
}

func (cfg *ServiceConfig) WriteService(path string, tmpl *template.Template) error {
	path = strings.Replace(path, "{name}", cfg.Name, 1)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	defer file.Close()

	err = tmpl.Execute(file, cfg)
	if err != nil {
		return err
	}

	return file.Close()
}
