#!/bin/bash
# Installs the OpenSurge fan curve as a root launch daemon.
# Reads the built binary from the repo and does not touch the gateway helper.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
src="$root/bin/opensurge-fan"
if [[ ! -x "$src" ]]; then
  echo "missing $src; build it first" >&2
  exit 1
fi
install_dir="/Library/Application Support/OpenSurge/bin"
mkdir -p "$install_dir" /Library/Logs/OpenSurge
install -m 0755 "$src" "$install_dir/opensurge-fan"
if [[ ! -f "/Library/Application Support/OpenSurge/fanguard.conf" ]]; then
  install -m 0644 "$root/packaging/fan/fanguard.conf" "/Library/Application Support/OpenSurge/fanguard.conf"
fi
if [[ ! -f "/Library/Application Support/OpenSurge/fan-mode" ]]; then
  printf 'curve\n' > "/Library/Application Support/OpenSurge/fan-mode"
  chmod 0644 "/Library/Application Support/OpenSurge/fan-mode"
fi
codesign --force --sign - "$install_dir/opensurge-fan"
install -m 0644 "$root/packaging/launchd/com.opensurge.fan.plist" /Library/LaunchDaemons/com.opensurge.fan.plist
launchctl bootout system/com.opensurge.fan 2>/dev/null || true
launchctl bootstrap system /Library/LaunchDaemons/com.opensurge.fan.plist
launchctl enable system/com.opensurge.fan
launchctl kickstart -k system/com.opensurge.fan
echo "opensurge-fan installed"
