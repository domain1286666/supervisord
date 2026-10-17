#!/bin/sh
# Install a released binary or a locally supplied build. Run: sudo sh install.sh --binary ./supervisord
set -eu
umask 077

repo=${SUPERVISORD_REPO:-ochinchina/supervisord}
version=${SUPERVISORD_VERSION:-latest}
port=8002
bind=0.0.0.0
binary=
bin_dir=/usr/local/bin
conf_dir=/etc/supervisord
start=1
temp_dir=

usage() {
  cat <<'USAGE'
Usage: sudo sh install.sh [options]
  --binary PATH     Install a local binary instead of downloading a release
  --repo OWNER/REPO Release repository (default ochinchina/supervisord)
  --version TAG     Release tag (default latest)
  --port PORT       Port for a newly created configuration (default 8002)
  --bind ADDRESS    Bind address for a newly created configuration (default 0.0.0.0)
  --no-start        Install and enable service without starting it
Existing configuration, password and certificate are retained.
USAGE
}
die() { printf '%s\n' "$*" >&2; exit 1; }
while [ "$#" -gt 0 ]; do
  case "$1" in
    --binary|--repo|--version|--port|--bind)
      [ "$#" -ge 2 ] || die "Missing value for $1"
      case "$1" in --binary) binary=$2;; --repo) repo=$2;; --version) version=$2;; --port) port=$2;; --bind) bind=$2;; esac
      shift 2;;
    --no-start) start=0; shift;;
    -h|--help) usage; exit 0;;
    *) die "Unknown argument: $1";;
  esac
done

[ "$(uname -s)" = Linux ] || die 'This installer supports Linux with systemd.'
[ "$(id -u)" -eq 0 ] || die 'Run this installer with sudo or as root.'
command -v systemctl >/dev/null 2>&1 || die 'systemd is required; for containers run supervisord -c /path/to/supervisord.conf directly.'
case "$port" in ''|*[!0-9]*) die 'Port must be numeric';; esac
[ "$port" -ge 1 ] && [ "$port" -le 65535 ] || die 'Port must be between 1 and 65535.'
case "$repo" in *[!A-Za-z0-9_./-]*|/*|*..*) die 'Invalid release repository';; esac
case "$version" in *[!A-Za-z0-9_.-]*|*..*) die 'Invalid release tag';; esac

cleanup() { if [ -n "$temp_dir" ] && [ -d "$temp_dir" ]; then rm -f "$temp_dir/supervisord" "$temp_dir/checksums.txt" "$temp_dir/selected.checksum"; rmdir "$temp_dir" 2>/dev/null || :; fi; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' HUP TERM
temp_dir=$(mktemp -d)
if [ -z "$binary" ]; then
  command -v curl >/dev/null 2>&1 || die 'curl is required for downloading; alternatively use --binary PATH.'
  command -v sha256sum >/dev/null 2>&1 || die 'sha256sum is required for release verification.'
  case "$(uname -m)" in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; armv7l) arch=armv7;; *) die 'Unsupported CPU architecture';; esac
  asset=supervisord_linux_$arch
  if [ "$version" = latest ]; then base="https://github.com/$repo/releases/latest/download"; else base="https://github.com/$repo/releases/download/$version"; fi
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --retry 3 --connect-timeout 15 --max-time 300 "$base/$asset" -o "$temp_dir/supervisord"
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --retry 3 --connect-timeout 15 --max-time 60 "$base/checksums.txt" -o "$temp_dir/checksums.txt"
  expected=$(awk -v name="$asset" '$2 == name || $2 == "*"name {print $1}' "$temp_dir/checksums.txt")
  [ "${#expected}" -eq 64 ] || die 'Release checksum is missing or ambiguous.'
  printf '%s  supervisord\n' "$expected" > "$temp_dir/selected.checksum"
  (cd "$temp_dir" && sha256sum -c selected.checksum) || die 'Binary checksum mismatch.'
  binary=$temp_dir/supervisord
else
  [ -f "$binary" ] || die "Local binary not found: $binary"
  cp "$binary" "$temp_dir/supervisord"
  binary=$temp_dir/supervisord
fi
chmod 0755 "$binary"
# Initialize before replacing the running executable; a legacy binary will fail safely here.
mkdir -p "$conf_dir" "$bin_dir"
chmod 0700 "$conf_dir"
"$binary" init --output "$conf_dir/supervisord.conf" --port "$port" --bind "$bind"
install -m 0755 "$binary" "$bin_dir/.supervisord.new"
mv -f "$bin_dir/.supervisord.new" "$bin_dir/supervisord"
cat > /etc/systemd/system/go-supervisord.service <<'UNIT'
[Unit]
Description=Supervisord secure process manager
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/supervisord -n -c /etc/supervisord/supervisord.conf
Restart=on-failure
RestartSec=3
TimeoutStopSec=90
LimitNOFILE=65536
UMask=0077

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable go-supervisord.service
if [ "$start" -eq 1 ]; then
  systemctl restart go-supervisord.service
  systemctl is-active --quiet go-supervisord.service || die 'Service failed; inspect journalctl -u go-supervisord.service.'
fi
printf '\nInstallation complete. New installation URL: https://SERVER_IP:%s\nConfiguration: %s/supervisord.conf\nExisting installations retain their configured listen address.\n' "$port" "$conf_dir"
printf 'Verify the certificate SHA-256 fingerprint printed above before accepting the self-signed certificate.\n'
