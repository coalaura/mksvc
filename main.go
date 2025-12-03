package main

import (
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
	"github.com/coalaura/plain"
)

var log = plain.New()

type Arguments struct {
	Name        string `arg:"" help:"Name of the service and its executable."`
	Path        string `arg:"" help:"Path to the service root and its working directory."`
	Interactive bool   `short:"i" help:"Enable interactive configuration mode."`
}

func main() {
	var args Arguments

	kong.Parse(&args)

	cfg := NewServiceConfig(args.Name, args.Path)

	if args.Interactive {
		var err error

		cfg.NeedsNetwork, err = log.Confirm("NeedsNetwork", cfg.NeedsNetwork)
		log.MustFail(err)

		cfg.NeedsListening, err = log.Confirm("NeedsListening", cfg.NeedsListening)
		log.MustFail(err)

		cfg.NeedsExecMemory, err = log.Confirm("NeedsExecMemory", cfg.NeedsExecMemory)
		log.MustFail(err)

		cfg.NeedsWritableFiles, err = log.Confirm("NeedsWritableFiles", cfg.NeedsWritableFiles)
		log.MustFail(err)

		cfg.NeedsPublicTmp, err = log.Confirm("NeedsPublicTmp", cfg.NeedsPublicTmp)
		log.MustFail(err)

		cfg.NeedsDevices, err = log.Confirm("NeedsDevices", cfg.NeedsDevices)
		log.MustFail(err)

		cfg.NeedsSubprocess, err = log.Confirm("NeedsSubprocess", cfg.NeedsSubprocess)
		log.MustFail(err)
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
