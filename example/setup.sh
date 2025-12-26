#!/bin/bash

set -euo pipefail

# --- HARDWARE PERMISSIONS ---
# If this service requires hardware access, you likely need a udev rule
# to assign ownership to the 'example' user.
# Example: /etc/udev/rules.d/99-example.rules
# SUBSYSTEM=="usb", ATTRS{idVendor}=="XXXX", OWNER="example"
# ----------------------------

echo "Linking sysusers config..."

mkdir -p /etc/sysusers.d

if [ -f /etc/sysusers.d/example.conf ]; then
    rm /etc/sysusers.d/example.conf
fi

ln -s "/var/example/conf/example.conf" /etc/sysusers.d/example.conf

echo "Creating user..."

systemd-sysusers

echo "Linking unit..."

if [ -f /etc/systemd/system/example.service ]; then
    rm /etc/systemd/system/example.service
fi

systemctl link "/var/example/conf/example.service"

if command -v logrotate >/dev/null 2>&1; then
    echo "Linking logrotate config..."

    if [ -f /etc/logrotate.d/example ]; then
        rm /etc/logrotate.d/example
    fi

    ln -s "/var/example/conf/example_logs.conf" /etc/logrotate.d/example
else
    echo "Logrotate not found, skipping..."
fi

echo "Reloading daemon..."

systemctl daemon-reload
systemctl enable example

echo "Fixing initial permissions..."

mkdir -p "/var/example/logs"

chown -R example:example "/var/example"

find "/var/example" -type d -exec chmod 755 {} +
find "/var/example" -type f -exec chmod 644 {} +

chmod +x "/var/example/example"

echo "Setup complete, starting service..."

service example restart

echo "Done."
