package main

import (
	_ "embed"
	"os"
	"text/template"
)

//go:embed templates/help.tmpl
var HelpStr string

func help() {
	t := template.Must(template.New("help").Parse(HelpStr))

	t.Execute(os.Stdout, map[string]string{
		"U": "\033[4m",
		"B": "\033[1m",
		"R": "\033[0m",
	})

	os.Exit(0)
}
