package main

import (
	_ "embed"
	"os"
	"text/template"
)

//go:embed templates/help.tmpl
var HelpStr string

func version() {
	log.Printf("\033[1mmksvc\033[0m version \033[4m%s\033[0m\n", Version)
}

func help() {
	t := template.Must(template.New("help").Parse(HelpStr))

	t.Execute(os.Stdout, map[string]string{
		"U": "\033[4m",
		"B": "\033[1m",
		"R": "\033[0m",
	})
}
