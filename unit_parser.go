package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const maxUnitLineBytes = 1024 * 1024

type unitCustomization struct {
	unit       map[string][]string
	service    map[string][]string
	install    map[string][]string
	hasInstall bool
}

var preservedUnitKeys = map[string]bool{
	"After": true, "Before": true, "Requires": true, "Wants": true,
	"Requisite": true, "BindsTo": true, "PartOf": true, "Conflicts": true,
}

var preservedInstallKeys = map[string]bool{
	"WantedBy":   true,
	"RequiredBy": true,
}

func (cfg *ServiceConfig) PreserveCustom(path string) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		return err
	}

	defer file.Close()

	custom := unitCustomization{
		unit:    make(map[string][]string),
		service: make(map[string][]string),
		install: make(map[string][]string),
	}

	defaults := defaultLimits()

	scanner := bufio.NewScanner(file)

	scanner.Buffer(make([]byte, 4096), maxUnitLineBytes+1)

	var (
		logical    strings.Builder
		section    string
		continuing bool
		lineNumber int
	)

	for scanner.Scan() {
		lineNumber++

		line := strings.TrimSpace(scanner.Text())
		if strings.ContainsAny(line, "\x00\r") {
			return fmt.Errorf("%s:%d: invalid control character", path, lineNumber)
		}

		// Comments and empty lines do not interrupt a continued assignment.
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		continuing = hasContinuation(line)
		if continuing {
			line = line[:len(line)-1]
		}

		if logical.Len()+len(line)+1 > maxUnitLineBytes {
			return fmt.Errorf("%s:%d: logical line exceeds %d bytes", path, lineNumber, maxUnitLineBytes)
		}

		logical.WriteString(line)

		if continuing {
			logical.WriteByte(' ')

			continue
		}

		err = custom.parseLine(logical.String(), &section, defaults)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}

		logical.Reset()
	}

	err = scanner.Err()
	if err != nil {
		return fmt.Errorf("%s:%d: %w", path, lineNumber+1, err)
	}

	if continuing {
		return fmt.Errorf("%s:%d: unfinished line continuation", path, lineNumber)
	}

	for key, values := range custom.service {
		if original, exists := defaults[key]; exists && len(values) == 1 && values[0] == original {
			delete(custom.service, key)

			continue
		}

		delete(defaults, key)
	}

	cfg.After = removeManagedTargets(strings.Join(custom.unit["After"], " "))
	cfg.Requires = removeManagedTargets(strings.Join(custom.unit["Requires"], " "))

	delete(custom.unit, "After")
	delete(custom.unit, "Requires")

	cfg.UnitCustom = custom.unit
	cfg.Custom = custom.service
	cfg.InstallCustom = custom.install
	cfg.HasInstallSection = custom.hasInstall
	cfg.Defaults = defaults

	return nil
}

func (custom *unitCustomization) parseLine(line string, section *string, defaults map[string]string) error {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
		*section = line[1 : len(line)-1]

		switch *section {
		case "Unit", "Service":
		case "Install":
			custom.hasInstall = true
		default:
			return fmt.Errorf("unsupported section [%s]", *section)
		}

		return nil
	}

	key, value, found := strings.Cut(line, "=")

	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)

	if !found || !directiveRgx.MatchString(key) {
		return fmt.Errorf("invalid directive %q", line)
	}

	switch *section {
	case "Unit":
		if key == "Description" || key == "StartLimitBurst" || key == "StartLimitIntervalSec" {
			return nil
		}

		if preservedUnitKeys[key] {
			custom.unit[key] = append(custom.unit[key], value)

			return nil
		}
	case "Service":
		if managedKeys[key] {
			return nil
		}

		if preservedCustomKeys[key] {
			custom.service[key] = append(custom.service[key], value)

			return nil
		}

		if original, exists := defaults[key]; exists && original == value {
			return nil
		}
	case "Install":
		if preservedInstallKeys[key] {
			custom.install[key] = append(custom.install[key], value)

			return nil
		}
	default:
		return fmt.Errorf("directive %s outside a supported section", key)
	}

	return fmt.Errorf("refusing to preserve unsupported directive [%s] %s", *section, key)
}

func hasContinuation(line string) bool {
	backslashes := 0

	for index := len(line) - 1; index >= 0 && line[index] == '\\'; index-- {
		backslashes++
	}

	return backslashes%2 != 0
}
