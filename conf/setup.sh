#!/bin/bash

set -euo pipefail

echo "Linking sysusers config..."

mkdir -p /etc/sysusers.d

if [ ! -f /etc/sysusers.d/test.conf ]; then
    ln -s "/var/test/conf/test.conf" /etc/sysusers.d/test.conf
fi

echo "Creating user..."
systemd-sysusers

echo "Linking unit..."
rm /etc/systemd/system/test.service

systemctl link "/var/test/conf/test.service"

echo "Reloading daemon..."
systemctl daemon-reload
systemctl enable test

echo "Fixing initial permissions..."
chown -R test:test "/var/test"

find "/var/test" -type d -exec chmod 755 {} +
find "/var/test" -type f -exec chmod 644 {} +

chmod +x "/var/test/test"

echo "Setup complete, starting service..."

service test start

echo "Done."
