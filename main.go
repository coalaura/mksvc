package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/coalaura/plain"
)

var Version = "dev"

var log = plain.New()

type CLI struct {
	Name string `arg:"" optional:"" help:"Name of the service and executable."`
	Path string `arg:"" optional:"" help:"Path to the service root directory."`

	Interactive bool `short:"i" help:"Enable interactive configuration mode."`
	DryRun      bool `short:"n" name:"dry-run" help:"Preview generated files without writing."`

	// Core options
	Network         *bool `name:"network" negatable:"" help:"Network access."`
	Listening       *bool `name:"listening" negatable:"" help:"Server mode (port binding)."`
	PrivilegedPorts *bool `name:"privileged-ports" negatable:"" help:"Ports below 1024."`
	ExecMemory      *bool `name:"exec-memory" negatable:"" help:"JIT/executable memory."`
	WritableFiles   *bool `name:"writable" negatable:"" help:"Writable working directory."`
	RuntimeDir      *bool `name:"runtime-dir" negatable:"" help:"Runtime directory (/run)."`
	Devices         *bool `name:"devices" negatable:"" help:"Hardware device access."`
	FullDevices     *bool `name:"full-devices" negatable:"" help:"Unrestricted device access."`
	Subprocess      *bool `name:"subprocess" negatable:"" help:"Shell/subprocess execution."`
	SeparateLogDir  *bool `name:"log-dir" negatable:"" help:"Separate logs subdirectory."`

	// Advanced security
	LocalhostOnly *bool `name:"localhost-only" negatable:"" help:"Restrict network to localhost."`
	PrivateUsers  *bool `name:"private-users" negatable:"" help:"User namespace isolation."`

	// Resource limits
	CPUQuota  string `name:"cpu-quota" help:"CPU quota (e.g., 200%% for 2 cores)."`
	MemoryMax string `name:"memory-max" help:"Memory limit (e.g., 2G, 512M)."`

	// Environment
	EnvFile string `name:"env-file" help:"Path to environment file."`

	Help    bool `short:"h" help:"Show detailed help."`
	Version bool `short:"v" help:"Print version."`
}

func main() {
	go log.WaitForInterrupt(true)

	var cli CLI

	kong.Parse(&cli,
		kong.Name("mksvc"),
		kong.Description("Hardened systemd service generator"),
		kong.NoDefaultHelp(),
	)

	if cli.Version {
		version()

		return
	}

	if cli.Help {
		help()

		return
	}

	confDir := "conf"
	configPath := filepath.Join(confDir, "svc.yml")

	cfg, err := LoadConfig(configPath)
	if err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: Could not load config: %v\n", err)
	}

	if cfg != nil {
		log.Printf("Loaded existing configuration from %s\n", configPath)

		if cli.Name == "" {
			cli.Name = cfg.Name
		}

		if cli.Path == "" {
			cli.Path = cfg.Path
		}
	}

	if cli.Name == "" || cli.Path == "" {
		log.Println("Usage: mksvc <name> <path> [options]")
		log.Println("Run 'mksvc -h' for detailed help.")

		os.Exit(1)
	}

	if cfg == nil {
		cfg = NewServiceConfig(cli.Name, cli.Path)
	} else {
		cfg.Name = cli.Name
		cfg.Path = cli.Path

		cfg.UpdateLabel()
	}

	applyOverrides(cfg, &cli)

	if cli.Interactive {
		runInteractive(cfg)
	}

	servicePath := filepath.Join(confDir, cfg.Name+".service")

	if err := cfg.PreserveCustom(servicePath); err != nil {
		log.Printf("Warning: Could not read existing service file: %v\n", err)
	} else if len(cfg.Custom) > 0 {
		log.Printf("Preserved %d custom configuration lines.\n", len(cfg.Custom))
	}

	cfg.ApplyDefaultAfter()
	cfg.ApplyDeviceDefaults()

	if cli.DryRun {
		dryRun(cfg, confDir)

		return
	}

	err = writeConfigs(cfg, confDir, configPath, servicePath)
	log.MustExit(err)

	log.Println("Done. Run 'sudo bash conf/setup.sh' to install.")
}

func applyOverrides(cfg *ServiceConfig, cli *CLI) {
	// Core options
	if cli.Network != nil {
		cfg.Network = *cli.Network
	}

	if cli.Listening != nil {
		cfg.Listening = *cli.Listening
	}

	if cli.PrivilegedPorts != nil {
		cfg.PrivilegedPorts = *cli.PrivilegedPorts
	}

	if cli.ExecMemory != nil {
		cfg.ExecMemory = *cli.ExecMemory
	}

	if cli.WritableFiles != nil {
		cfg.WritableFiles = *cli.WritableFiles
	}

	if cli.RuntimeDir != nil {
		cfg.RuntimeDir = *cli.RuntimeDir
	}

	if cli.Devices != nil {
		cfg.Devices = *cli.Devices
	}

	if cli.FullDevices != nil {
		cfg.FullDevices = *cli.FullDevices
	}

	if cli.Subprocess != nil {
		cfg.Subprocess = *cli.Subprocess
	}

	if cli.SeparateLogDir != nil {
		cfg.SeparateLogDir = *cli.SeparateLogDir
	}

	// Advanced security
	if cli.LocalhostOnly != nil {
		cfg.LocalhostOnly = *cli.LocalhostOnly
	}

	if cli.PrivateUsers != nil {
		cfg.PrivateUsers = *cli.PrivateUsers
	}

	// Resource limits
	if cli.CPUQuota != "" {
		cfg.CPUQuota = cli.CPUQuota
	}

	if cli.MemoryMax != "" {
		cfg.MemoryMax = cli.MemoryMax
	}

	// Environment
	if cli.EnvFile != "" {
		cfg.EnvFile = cli.EnvFile
	}
}

func runInteractive(cfg *ServiceConfig) {
	log.Println("Interactive Configuration")
	log.Println("Press Enter to accept defaults.")

	// Network section
	cfg.Network = ask(
		"Network Access",
		"Required for internet/intranet. Disabling creates an airgapped namespace.",
		cfg.Network,
	)

	if cfg.Network {
		cfg.Listening = ask(
			"Server Mode",
			"Allows port binding. Waits for network-online.target before starting.",
			cfg.Listening,
		)

		if cfg.Listening {
			cfg.PrivilegedPorts = ask(
				"Privileged Ports",
				"Allow binding to ports <1024 (80/443) via CAP_NET_BIND_SERVICE.",
				cfg.PrivilegedPorts,
			)
		} else {
			cfg.PrivilegedPorts = false
		}

		cfg.LocalhostOnly = ask(
			"Localhost Only",
			"Restrict network to 127.0.0.0/8 and ::1. For local database access.",
			cfg.LocalhostOnly,
		)
	} else {
		cfg.Listening = false
		cfg.PrivilegedPorts = false
		cfg.LocalhostOnly = false
	}

	// Filesystem section
	cfg.ExecMemory = ask(
		"Executable Memory",
		"Required for JIT runtimes (Node, Java) or Go WASM (wazero).",
		cfg.ExecMemory,
	)

	cfg.WritableFiles = ask(
		"Writable Directory",
		"Allows service to modify files in its working directory.",
		cfg.WritableFiles,
	)

	cfg.RuntimeDir = ask(
		"Runtime Directory",
		"Creates /run/<name> for sockets or PID files.",
		cfg.RuntimeDir,
	)

	// Hardware section
	cfg.Devices = ask(
		"Hardware Devices",
		"Access to /dev (USB, serial, GPU).",
		cfg.Devices,
	)

	if cfg.Devices {
		cfg.FullDevices = ask(
			"Full Device Access",
			"Disables device sandboxing. Use if standard rules fail.",
			cfg.FullDevices,
		)
	} else {
		cfg.FullDevices = false
	}

	// Process section
	cfg.Subprocess = ask(
		"Subprocesses",
		"Allow spawning shell commands or external binaries.",
		cfg.Subprocess,
	)

	cfg.PrivateUsers = ask(
		"Private Users",
		"User namespace isolation. May break user lookups or capabilities.",
		cfg.PrivateUsers,
	)

	// Output section
	cfg.SeparateLogDir = ask(
		"Separate Logs",
		"Organize logs into a 'logs' subdirectory.",
		cfg.SeparateLogDir,
	)

	// Resource limits
	log.Println()
	log.Println("Resource Limits (leave empty for no limit)")

	cfg.CPUQuota = askString("  CPU Quota (e.g., 200%)", cfg.CPUQuota)
	cfg.MemoryMax = askString("  Memory Max (e.g., 2G)", cfg.MemoryMax)

	log.Println()
}

func ask(title, desc string, def bool) bool {
	log.Println()

	log.Println(title)
	log.Printf("  %s\n", desc)

	val, err := log.ConfirmWithEcho("  Enable", def, " ")
	log.MustExit(err)

	return val
}

func askString(prompt, def string) string {
	display := def

	if display == "" {
		display = "none"
	}

	log.Printf("%s [%s]: ", prompt, display)

	val, err := log.Read("", 12)
	log.MustFail(err)

	val = strings.TrimSpace(val)

	if val == "" {
		return def
	}

	return val
}

func dryRun(cfg *ServiceConfig, confDir string) {
	log.Println("Dry run - no files written.")
	log.Println()
	log.Println("Configuration:")
	log.Printf("  Name:             %s\n", cfg.Name)
	log.Printf("  Path:             %s\n", cfg.Path)
	log.Println()
	log.Println("Core Options:")
	log.Printf("  Network:          %v\n", cfg.Network)
	log.Printf("  Listening:        %v\n", cfg.Listening)
	log.Printf("  PrivilegedPorts:  %v\n", cfg.PrivilegedPorts)
	log.Printf("  ExecMemory:       %v\n", cfg.ExecMemory)
	log.Printf("  WritableFiles:    %v\n", cfg.WritableFiles)
	log.Printf("  RuntimeDir:       %v\n", cfg.RuntimeDir)
	log.Printf("  Devices:          %v\n", cfg.Devices)
	log.Printf("  FullDevices:      %v\n", cfg.FullDevices)
	log.Printf("  Subprocess:       %v\n", cfg.Subprocess)
	log.Printf("  SeparateLogDir:   %v\n", cfg.SeparateLogDir)
	log.Println()
	log.Println("Advanced Security:")
	log.Printf("  LocalhostOnly:    %v\n", cfg.LocalhostOnly)
	log.Printf("  PrivateUsers:     %v\n", cfg.PrivateUsers)
	log.Println()
	log.Println("Resource Limits:")
	log.Printf("  CPUQuota:         %s\n", valueOr(cfg.CPUQuota, "none"))
	log.Printf("  MemoryMax:        %s\n", valueOr(cfg.MemoryMax, "none"))

	if cfg.EnvFile != "" {
		log.Printf("  EnvFile:          %s\n", cfg.EnvFile)
	}

	log.Println()
	log.Println("Would generate:")
	log.Printf("  %s/%s.service\n", confDir, cfg.Name)
	log.Printf("  %s/%s.conf\n", confDir, cfg.Name)
	log.Printf("  %s/%s_logs.conf\n", confDir, cfg.Name)
	log.Printf("  %s/setup.sh\n", confDir)
	log.Printf("  %s/uninstall.sh\n", confDir)
	log.Printf("  %s/svc.yml\n", confDir)
}

func valueOr(val, fallback string) string {
	if val == "" {
		return fallback
	}

	return val
}

func writeConfigs(cfg *ServiceConfig, confDir, configPath, servicePath string) error {
	log.Println("Writing configs...")

	if _, err := os.Stat(confDir); os.IsNotExist(err) {
		err = os.Mkdir(confDir, 0755)
		if err != nil {
			return err
		}
	}

	err := cfg.SaveConfig(configPath)
	if err != nil {
		return err
	}

	err = cfg.WriteTemplate(servicePath, ServiceTmpl)
	if err != nil {
		return err
	}

	err = cfg.WriteTemplate(filepath.Join(confDir, "{name}.conf"), UserTmpl)
	if err != nil {
		return err
	}

	err = cfg.WriteTemplate(filepath.Join(confDir, "setup.sh"), SetupTmpl)
	if err != nil {
		return err
	}

	err = cfg.WriteTemplate(filepath.Join(confDir, "uninstall.sh"), UninstallTmpl)
	if err != nil {
		return err
	}

	return cfg.WriteTemplate(filepath.Join(confDir, "{name}_logs.conf"), LogrotateTmpl)
}
