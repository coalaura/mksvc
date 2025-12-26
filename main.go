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

	for _, arg := range os.Args {
		if arg == "-h" || arg == "--help" {
			help()
		}
	}

	var args Arguments
	kong.Parse(&args)

	cfg := NewServiceConfig(args.Name, args.Path)
	servicePath := filepath.Join("conf", args.Name+".service")

	if args.Interactive {
		configure(cfg)
	}

	err := cfg.PreserveCustom(servicePath)
	if err != nil {
		log.Printf("Warning: Could not read existing config: %v\n", err)
	} else if len(cfg.Custom) > 0 {
		log.Printf("Found %d custom configuration lines.\n", len(cfg.Custom))
	}

	cfg.ApplyDefaultAfter()
	cfg.ApplyDeviceDefaults()

	log.Println("Writing configs...")

	if _, err := os.Stat("conf"); os.IsNotExist(err) {
		err = os.Mkdir("conf", 0755)
		log.MustFail(err)
	}

	err = cfg.WriteService(servicePath, ServiceTmpl)
	log.MustFail(err)

	err = cfg.WriteService(filepath.Join("conf", "{name}.conf"), UserTmpl)
	log.MustFail(err)

	err = cfg.WriteService(filepath.Join("conf", "setup.sh"), SetupTmpl)
	log.MustFail(err)

	err = cfg.WriteService(filepath.Join("conf", "{name}_logs.conf"), LogrotateTmpl)
	log.MustFail(err)

	log.Println("Done.")
}

func configure(cfg *ServiceConfig) {
	log.Println("Interactive Configuration")
	log.Println("Press Enter to accept defaults.")

	cfg.NeedsNetwork = ask(
		"Network Access",
		"Required for internet/intranet access. Disabling creates an airgapped namespace.",
		cfg.NeedsNetwork,
	)

	if cfg.NeedsNetwork {
		cfg.NeedsListening = ask(
			"Server Mode (Listening)",
			"Forces dependency on network-online.target. Required for web servers/APIs.",
			cfg.NeedsListening,
		)
	} else {
		cfg.NeedsListening = false
	}

	cfg.NeedsExecMemory = ask(
		"JIT/Executable Memory",
		"Required for Java, Node.js, Go WASM (wazero) or plugins.",
		cfg.NeedsExecMemory,
	)

	cfg.NeedsWritableFiles = ask(
		"Writable Working Directory",
		"Allows the service to modify its own files. Not required for logging.",
		cfg.NeedsWritableFiles,
	)

	cfg.NeedsRuntimeDir = ask(
		"Runtime Directory (IPC)",
		"Creates /run/"+cfg.Name+" for sockets or PID files.",
		cfg.NeedsRuntimeDir,
	)

	cfg.NeedsDevices = ask(
		"Hardware Devices",
		"Grants access to /dev (USB, GPU, serial ports).",
		cfg.NeedsDevices,
	)

	if cfg.NeedsDevices {
		cfg.FullDevices = ask(
			"Complex Device Access?",
			"Enable if using libusb, raw HID or if standard device rules fail.\n  (Disables Systemd device sandboxing; relies on file permissions/udev).",
			cfg.FullDevices,
		)
	} else {
		cfg.FullDevices = false
	}

	cfg.NeedsSubprocess = ask(
		"Subprocesses",
		"Allows the service to spawn shell commands or other binaries.",
		cfg.NeedsSubprocess,
	)

	cfg.SeparateLogDir = ask(
		"Separate Logs Directory",
		"Organize logs into a 'logs' subdirectory to keep the root clean.",
		cfg.SeparateLogDir,
	)

	log.Println("")
}

func ask(title, desc string, def bool) bool {
	log.Println()
	log.Println(title)
	log.Printf("  %s\n", desc)

	val, err := log.Confirm("  Enable", def)
	log.MustFail(err)

	return val
}
