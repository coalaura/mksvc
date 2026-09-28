<picture>
  <source media="(prefers-color-scheme: dark)" srcset=".github/banner.svg">
  <source media="(prefers-color-scheme: light)" srcset=".github/banner-light.svg">
  <img alt="mksvc - hardened systemd service generator" src=".github/banner-light.svg">
</picture>

A hardened, opinionated Systemd service generator for modern Linux deployments.

`mksvc` creates secure-by-default `.service` files that lock down the filesystem, network and kernel capabilities. It manages the full lifecycle of a service configuration: generating the unit file, creating a dedicated system user, configuring log rotation and generating a setup script; all while intelligently preserving manual customizations on subsequent runs.

## Installation

Download the latest binary and `checksums.txt` from the [Releases Page](https://github.com/coalaura/mksvc/releases), or install a verified prebuilt binary with one command:

```bash
curl -sL https://src.ws2.sh/mksvc/install.sh | bash
```

## Usage

Run `mksvc` in the deployed service root. The path must be below `/opt`, `/srv`, `/var/lib` or `/usr/local/lib`, and the executable must have the same name as the service.

```bash
# Generate configs interactively
mksvc my-app /opt/my-app -i

# Apply the configuration (requires sudo)
sudo bash conf/setup.sh
```

Setup makes the deployed executable and generated configuration root-owned. Run future regeneration as root from the same service directory, then rerun `conf/setup.sh`.

### Generated Artifacts

The tool creates a `conf/` directory containing:

1. **`my-app.service`**: The Systemd unit file (Hardened).
2. **`my-app.conf`**: Sysusers configuration to create the `my-app` user/group.
3. **`my-app_logs.conf`**: Logrotate configuration for file logging (omitted with `--journald`).
4. **`setup.sh`**: An idempotent script to install root-owned units, create users and configure log rotation.
5. **`uninstall.sh`**: Removes installed configuration and identities created by mksvc.
6. **`svc.yml`**: Saved configuration for subsequent runs.

### Logging

File logging remains the default. Standard output and error append to `logs/<name>.log`, or `<name>.log` with `--no-log-dir`. Setup preserves existing log contents and rotates logs as root. The log directory is root-owned and cannot be modified by the service; the active log file is owned by `root:<service>` with mode `0660`, allowing the service to write its contents without replacing its pathname. Only that file, not the entire log directory, is writable inside the sandbox. Applications that create additional log files should use stdout/stderr instead.

Use `--journald` to send stdout/stderr to the system journal instead. Interactive mode asks about journald first and only asks file-logging questions when it is disabled. This choice is saved in `conf/svc.yml`; `--no-journald` switches back to files.

```bash
mksvc my-app /opt/my-app --journald
sudo bash conf/setup.sh
journalctl -u my-app.service
```

Journald mode creates no log files or logrotate configuration and removes a stale generated `conf/<name>_logs.conf`. Setup removes an installed rotation rule bearing mksvc's ownership header, while leaving existing logs intact. If switching directly from an older release with an unmarked rotation rule, review and remove `/etc/logrotate.d/<name>` before running setup; setup refuses to remove an unrecognized rule automatically.

## Customization & Persistence

`mksvc` is designed to run repeatedly without destroying your work.

1. **Managed Keys**: Security attributes (e.g., `ProtectSystem`, `SystemCallFilter`) are owned by the tool. They are reset based on your interactive choices. `DeviceAllow` rules are regenerated from the device options, so disabling device access also removes old rules.
2. **Custom Keys**: `Environment` values, managed timeout overrides, and custom `After` and `Requires` targets are preserved. Other unmanaged directives are rejected because they are unsafe to import automatically.

### Example
If you manually add this to `conf/my-app.service`:

```ini
[Service]
Environment=API_KEY=12345
TimeoutStartSec=600
```

Running `mksvc` again will update the security sandbox settings but keep your `Environment` value and `TimeoutStartSec` override.

## Security Features

* **Filesystem**: Root is read-only (`ProtectSystem=strict`). Working directory is read-only by default.
* **Process**: No new privileges, restricted namespaces. Shells/subprocess capabilities are opt-in.
* **Network**: Offline/Airgapped by default (`PrivateNetwork=yes`). Optional "Server Mode" for binding ports.
* **Kernel**: Logs, modules and tunables are protected. `/dev` is private.
* **Memory**: `MemoryDenyWriteExecute` enabled by default (WASM/JIT can opt-in).
* **Ownership**: Application code, installed policy and log directory entries remain root-owned. Only the active log file's contents, the optional `data` directory and an explicitly enabled application config file are service-writable.

### Example security analysis

The included `example/example.service` (default mksvc options) scores **0.5 (SAFE)** with `systemd-analyze security --offline=1`:

![systemd-analyze security report](.github/analyze.png)
