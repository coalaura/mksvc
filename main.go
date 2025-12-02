package main

import (
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/coalaura/plain"
)

var log = plain.New()

var CLI struct {
	Name        string `arg:"" help:"Name of the service and its executable."`
	Path        string `arg:"" help:"Path to the service root and its working directory."`
	Interactive bool   `short:"i" help:"Enable interactive configuration mode."`
}

func main() {
	kong.Parse(&CLI)

	cfg := NewServiceConfig(CLI.Name, CLI.Path)

	if CLI.Interactive {
	}

	log.Println("Writing configs...")

	if _, err := os.Stat("conf"); os.IsNotExist(err) {
		err = os.Mkdir("conf", 0755)
		log.MustFail(err)
	}

	err := cfg.WriteService(filepath.Join("conf", "{name}.service"), ServiceTmpl)
	log.MustFail(err)

	err = cfg.WriteService(filepath.Join("conf", "{name}.conf"), UserTmpl)
	log.MustFail(err)

	err = cfg.WriteService(filepath.Join("conf", "setup.sh"), SetupTmpl)
	log.MustFail(err)

	log.Println("Done.")
}
