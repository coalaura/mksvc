# mksvc

A hardened, opinionated Systemd service generator for modern Linux deployments.

`mksvc` creates secure-by-default `.service` files that lock down the filesystem, network and kernel capabilities. It manages the full lifecycle of a service configuration: generating the unit file, creating a dedicated system user, configuring log rotation and generating a setup script; all while intelligently preserving manual customizations on subsequent runs.

## Installation

Download the latest binary and `checksums.txt` from the [Releases Page](https://github.com/coalaura/mksvc/releases), or install a verified prebuilt binary with one command:

```bash
curl -sL https://src.ws2.sh/mksvc/install.sh | sh
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
3. **`my-app_logs.conf`**: Logrotate configuration for efficient log management.
4. **`setup.sh`**: An idempotent script to install root-owned units, create users and configure log rotation.
5. **`uninstall.sh`**: Removes installed configuration and identities created by mksvc.
6. **`svc.yml`**: Saved configuration for subsequent runs.

## Customization & Persistence

`mksvc` is designed to run repeatedly without destroying your work.

1. **Managed Keys**: Security attributes (e.g., `ProtectSystem`, `SystemCallFilter`) are owned by the tool. They are reset based on your interactive choices.
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
* **Ownership**: Application code and installed policy remain root-owned. Only logs, the optional `data` directory, and an explicitly enabled application config file are service-writable.
