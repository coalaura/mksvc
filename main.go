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
	log.HandleInterrupt()

	var args Arguments
	kong.Parse(&args)

	cfg := NewServiceConfig(args.Name, args.Path)

	if args.Interactive {
		configure(cfg)
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

func configure(cfg *ServiceConfig) {
	log.Println("Interactive Configuration")
	log.Println("Press Enter to accept defaults.")

	cfg.NeedsNetwork = ask(
		"Network Access",
		"Required for internet access or communicating with other servers.",
		cfg.NeedsNetwork,
	)

	cfg.NeedsListening = ask(
		"Server Mode (Listening)",
		"Required if this service listens on a port (web servers, databases).",
		cfg.NeedsListening,
	)

	cfg.NeedsExecMemory = ask(
		"JIT/Executable Memory",
		"Required for runtimes like Java, Node.js, Python, or Go plugins.",
		cfg.NeedsExecMemory,
	)

	cfg.NeedsWritableFiles = ask(
		"Writable Working Directory",
		"Allows the service to write files/logs to its own folder.",
		cfg.NeedsWritableFiles,
	)

	cfg.NeedsDevices = ask(
		"Hardware Devices",
		"Grants access to /dev (USB, GPU, serial ports).",
		cfg.NeedsDevices,
	)

	cfg.NeedsSubprocess = ask(
		"Subprocesses",
		"Allows the service to spawn shell commands or other binaries.",
		cfg.NeedsSubprocess,
	)

	log.Println()
}

func ask(title, desc string, def bool) bool {
	log.Println()
	log.Println(title)
	log.Printf("  %s\n", desc)

	val, err := log.Confirm("  Enable", def)
	log.MustFail(err)

	return val
}
