#!/usr/bin/env bash
set -euo pipefail

REPO="r0n9/nodekeep"
DASHBOARD_SERVER=""
CLIENT_SECRET=""
NIC_ALLOWLIST=""
INSECURE=false
INIT=""
backup_dir=""
bin_path=""
service_file=""

usage() {
    cat <<EOF
Usage: install-agent.sh -s <dashboard-host:port> -p <agent-secret> [--insecure] [--nic eth0,en0]

Options:
  -s, --server   Dashboard address, for example dashboard.example.com:8008
  -p, --secret   Agent secret generated in the dashboard server list
  -n, --nic      Network interface allowlist, comma-separated, for example eth0,en0
  -d, --insecure Connect without TLS, for direct HTTP/h2c dashboard access
  -h, --help     Show this help
EOF
}

die() {
    echo "$*" >&2
    exit 1
}

run_as_root() {
    if [[ "${EUID}" -eq 0 ]]; then
        "$@"
        return
    fi
    if command -v sudo >/dev/null 2>&1; then
        sudo "$@"
        return
    fi
    die "This action requires root privileges. Please run as root or install sudo."
}

detect_linux_init() {
    init_path="$(readlink /sbin/init 2>/dev/null || true)"
    case "${init_path}" in
    *systemd*)
        INIT="systemd"
        service_file="/etc/systemd/system/nodekeep-agent.service"
        return
        ;;
    *openrc-init*|*busybox*)
        INIT="openrc"
        service_file="/etc/init.d/nodekeep-agent"
        return
        ;;
    esac

    if command -v systemctl >/dev/null 2>&1; then
        INIT="systemd"
        service_file="/etc/systemd/system/nodekeep-agent.service"
        return
    fi
    if command -v rc-service >/dev/null 2>&1 && command -v rc-update >/dev/null 2>&1; then
        INIT="openrc"
        service_file="/etc/init.d/nodekeep-agent"
        return
    fi
    die "Unsupported Linux init system. systemd or OpenRC is required."
}

download() {
    echo "Downloading nodekeep-agent package:"
    echo "  Target: ${release_os}_${release_arch}"
    echo "  URL: $1"
    echo "  Save as: $2"
    if command -v curl >/dev/null 2>&1; then
        echo "  Downloader: curl"
        curl -fL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        echo "  Downloader: wget"
        wget -O "$2" "$1"
    else
        die "curl or wget is required to download nodekeep-agent."
    fi
    echo "Download complete."
}

root_file_exists() {
    run_as_root test -f "$1"
}

backup_file() {
    local src="$1"
    local name="$2"
    if root_file_exists "${src}"; then
        run_as_root cp -p "${src}" "${backup_dir}/${name}"
    fi
}

restore_file() {
    local backup="$1"
    local dest="$2"
    local mode="$3"
    if root_file_exists "${backup}"; then
        run_as_root install -m "${mode}" "${backup}" "${dest}"
    fi
}

while [[ $# -gt 0 ]]; do
    case "$1" in
    -s|--server)
        DASHBOARD_SERVER="${2:-}"
        shift 2
        ;;
    -p|--secret)
        CLIENT_SECRET="${2:-}"
        shift 2
        ;;
    -n|--nic)
        NIC_ALLOWLIST="${2:-}"
        shift 2
        ;;
    -d|--insecure)
        INSECURE=true
        shift
        ;;
    -h|--help)
        usage
        exit 0
        ;;
    *)
        echo "Unknown argument: $1" >&2
        usage
        exit 1
        ;;
    esac
done

if [[ -z "${DASHBOARD_SERVER}" || -z "${CLIENT_SECRET}" ]]; then
    usage
    exit 1
fi

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${os}" in
linux)
    release_os="linux"
    install_dir="/opt/nodekeep/agent"
    detect_linux_init
    ;;
darwin)
    release_os="darwin"
    install_dir="/usr/local/nodekeep/agent"
    service_file="/Library/LaunchDaemons/com.nodekeep.agent.plist"
    ;;
*)
    die "Unsupported operating system: ${os}. This installer supports Linux and macOS."
    ;;
esac

arch="$(uname -m)"
case "${arch}" in
x86_64|amd64)
    release_arch="amd64"
    ;;
i386|i686|386)
    release_arch="386"
    ;;
aarch64|arm64)
    release_arch="arm64"
    ;;
armv5*|armv6*|armv7*|arm)
    release_arch="arm"
    ;;
mips64*)
    release_arch="mips64"
    ;;
mips*)
    release_arch="mips"
    ;;
s390x)
    release_arch="s390x"
    ;;
riscv64)
    release_arch="riscv64"
    ;;
*)
    die "Unsupported architecture: ${arch}"
    ;;
esac

if [[ "${release_os}" == "darwin" && "${release_arch}" != "amd64" && "${release_arch}" != "arm64" ]]; then
    die "Unsupported macOS architecture: ${arch}"
fi

archive="/tmp/nodekeep-agent_${release_os}_${release_arch}.tar.gz"
url="https://github.com/${REPO}/releases/latest/download/nodekeep-agent_${release_os}_${release_arch}.tar.gz"
bin_path="${install_dir}/nodekeep-agent"

echo "nodekeep-agent installer"
echo "  OS: ${release_os}"
echo "  Arch: ${release_arch}"
if [[ "${release_os}" == "linux" ]]; then
    echo "  Init: ${INIT}"
fi
echo "  Install directory: ${install_dir}"
echo "  Binary path: ${bin_path}"
echo "  Service file: ${service_file}"
echo "  Dashboard server: ${DASHBOARD_SERVER}"
if [[ -n "${NIC_ALLOWLIST}" ]]; then
    echo "  NIC allowlist: ${NIC_ALLOWLIST}"
else
    echo "  NIC allowlist: auto"
fi
if [[ "${INSECURE}" == "true" ]]; then
    echo "  Connection: insecure h2c"
else
    echo "  Connection: TLS"
fi
echo

run_as_root mkdir -p "${install_dir}"

download "${url}" "${archive}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}" "${archive}"' EXIT

echo "Extracting package: ${archive}"
tar xf "${archive}" -C "${tmp_dir}"
echo "Package extracted to: ${tmp_dir}"

agent_flags="-s ${DASHBOARD_SERVER} -p ${CLIENT_SECRET}"
if [[ "${INSECURE}" == "true" ]]; then
    agent_flags="-d ${agent_flags}"
fi
if [[ -n "${NIC_ALLOWLIST}" ]]; then
    agent_flags="${agent_flags} -n ${NIC_ALLOWLIST}"
fi

backup_dir="${install_dir}/backup"
run_as_root mkdir -p "${backup_dir}"
run_as_root rm -f "${backup_dir}/nodekeep-agent.bak" "${backup_dir}/service.bak"

stop_existing_service() {
    echo "Stopping existing nodekeep-agent service if present..."
    case "${release_os}" in
    linux)
        case "${INIT}" in
        systemd)
            run_as_root systemctl stop nodekeep-agent >/dev/null 2>&1 || true
            ;;
        openrc)
            run_as_root rc-service nodekeep-agent stop >/dev/null 2>&1 || true
            ;;
        esac
        ;;
    darwin)
        run_as_root launchctl bootout system "${service_file}" >/dev/null 2>&1 || true
        ;;
    esac
}

backup_current_install() {
    backup_file "${bin_path}" "nodekeep-agent.bak"
    backup_file "${service_file}" "service.bak"
}

install_new_binary() {
    echo "Installing binary to: ${bin_path}"
    run_as_root install -m 0755 "${tmp_dir}/nodekeep-agent" "${bin_path}"
}

rollback_install() {
    echo "nodekeep-agent install failed, rolling back previous installation..." >&2
    stop_existing_service
    if root_file_exists "${backup_dir}/nodekeep-agent.bak"; then
        restore_file "${backup_dir}/nodekeep-agent.bak" "${bin_path}" 0755
    else
        run_as_root rm -f "${bin_path}"
    fi
    if [[ "${release_os}" == "linux" && "${INIT}" == "openrc" ]]; then
        if root_file_exists "${backup_dir}/service.bak"; then
            restore_file "${backup_dir}/service.bak" "${service_file}" 0755
        else
            run_as_root rm -f "${service_file}"
        fi
    else
        if root_file_exists "${backup_dir}/service.bak"; then
            restore_file "${backup_dir}/service.bak" "${service_file}" 0644
        else
            run_as_root rm -f "${service_file}"
        fi
    fi
    if root_file_exists "${backup_dir}/nodekeep-agent.bak" && root_file_exists "${backup_dir}/service.bak"; then
        start_service || true
    fi
}

start_service() {
    echo "Starting nodekeep-agent service..."
    case "${release_os}" in
    linux)
        case "${INIT}" in
        systemd)
            run_as_root systemctl daemon-reload
            run_as_root systemctl enable nodekeep-agent
            run_as_root systemctl restart nodekeep-agent
            ;;
        openrc)
            run_as_root rc-update add nodekeep-agent default
            run_as_root rc-service nodekeep-agent restart
            ;;
        esac
        ;;
    darwin)
        run_as_root launchctl bootstrap system "${service_file}"
        run_as_root launchctl enable system/com.nodekeep.agent
        run_as_root launchctl kickstart -k system/com.nodekeep.agent
        ;;
    esac
}

check_service() {
    echo "Checking nodekeep-agent service status..."
    case "${release_os}" in
    linux)
        case "${INIT}" in
        systemd)
            run_as_root systemctl is-active --quiet nodekeep-agent
            ;;
        openrc)
            run_as_root rc-service nodekeep-agent status >/dev/null
            ;;
        esac
        ;;
    darwin)
        run_as_root launchctl print system/com.nodekeep.agent >/dev/null
        ;;
    esac
}

activate_install() {
    echo "Activating nodekeep-agent installation..."
    backup_current_install
    stop_existing_service
    install_new_binary
    if ! install_service_file; then
        rollback_install
        die "Failed to install nodekeep-agent service."
    fi
    if ! start_service; then
        rollback_install
        die "Failed to start nodekeep-agent service."
    fi
    if ! check_service; then
        rollback_install
        die "nodekeep-agent service did not become active."
    fi
}

install_service_file() {
    case "${release_os}" in
    linux)
        case "${INIT}" in
        systemd)
            install_systemd_service_file
            ;;
        openrc)
            install_openrc_service_file
            ;;
        esac
        ;;
    darwin)
        install_macos_service_file
        ;;
    esac
}

install_systemd_service_file() {
    service_tmp="${tmp_dir}/nodekeep-agent.service"
    cat >"${service_tmp}" <<EOF
[Unit]
Description=nodekeep Agent
After=syslog.target
After=network.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=${install_dir}/
ExecStart=${bin_path} ${agent_flags}
Restart=always
ProtectSystem=full
PrivateDevices=yes
PrivateTmp=yes
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF

    run_as_root install -m 0644 "${service_tmp}" "${service_file}"
}

install_openrc_service_file() {
    service_tmp="${tmp_dir}/nodekeep-agent.openrc"
    cat >"${service_tmp}" <<EOF
#!/sbin/openrc-run

name="nodekeep Agent"
description="nodekeep Agent"
command="${bin_path}"
command_args="${agent_flags}"
command_background=true
directory="${install_dir}"
pidfile="/run/\${RC_SVCNAME}.pid"
output_log="/var/log/nodekeep-agent.log"
error_log="/var/log/nodekeep-agent.err.log"

depend() {
    need net
}
EOF

    run_as_root install -m 0755 "${service_tmp}" "${service_file}"
}

xml_escape() {
    printf '%s' "$1" | sed \
        -e 's/&/\&amp;/g' \
        -e 's/</\&lt;/g' \
        -e 's/>/\&gt;/g' \
        -e 's/"/\&quot;/g' \
        -e "s/'/\&apos;/g"
}

install_macos_service_file() {
    extra_arg=""
    if [[ "${INSECURE}" == "true" ]]; then
        extra_arg="<string>-d</string>"
    fi

    escaped_install_dir="$(xml_escape "${install_dir}")"
    escaped_server="$(xml_escape "${DASHBOARD_SERVER}")"
    escaped_secret="$(xml_escape "${CLIENT_SECRET}")"
    escaped_nic="$(xml_escape "${NIC_ALLOWLIST}")"
    nic_args=""
    if [[ -n "${NIC_ALLOWLIST}" ]]; then
        nic_args="<string>-n</string>
    <string>${escaped_nic}</string>"
    fi

    service_tmp="${tmp_dir}/com.nodekeep.agent.plist"
    cat >"${service_tmp}" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.nodekeep.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>${bin_path}</string>
    ${extra_arg}
    <string>-s</string>
    <string>${escaped_server}</string>
    <string>-p</string>
    <string>${escaped_secret}</string>
    ${nic_args}
  </array>
  <key>WorkingDirectory</key>
  <string>${escaped_install_dir}</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/var/log/nodekeep-agent.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/nodekeep-agent.err.log</string>
</dict>
</plist>
EOF

    run_as_root install -m 0644 "${service_tmp}" "${service_file}"
}

print_success_instructions() {
    echo
    echo "nodekeep-agent installed successfully."
    echo "Binary: ${bin_path}"
    echo "Service file: ${service_file}"
    echo
    case "${release_os}" in
    linux)
        case "${INIT}" in
        systemd)
            cat <<EOF
Service status:
  sudo systemctl status nodekeep-agent

View logs:
  sudo journalctl -fu nodekeep-agent.service

Stop temporarily:
  sudo systemctl stop nodekeep-agent

Disable auto start:
  sudo systemctl disable nodekeep-agent

Uninstall:
  sudo systemctl disable --now nodekeep-agent
  sudo rm -f /etc/systemd/system/nodekeep-agent.service
  sudo systemctl daemon-reload
  sudo rm -rf ${install_dir}
EOF
            ;;
        openrc)
            cat <<EOF
Service status:
  sudo rc-service nodekeep-agent status

View logs:
  tail -f /var/log/nodekeep-agent.log /var/log/nodekeep-agent.err.log

Stop temporarily:
  sudo rc-service nodekeep-agent stop

Disable auto start:
  sudo rc-update del nodekeep-agent default

Uninstall:
  sudo rc-service nodekeep-agent stop
  sudo rc-update del nodekeep-agent default
  sudo rm -f /etc/init.d/nodekeep-agent
  sudo rm -rf ${install_dir}
EOF
            ;;
        esac
        ;;
    darwin)
        cat <<EOF
Service status:
  sudo launchctl print system/com.nodekeep.agent

View logs:
  tail -f /var/log/nodekeep-agent.log /var/log/nodekeep-agent.err.log

Stop temporarily:
  sudo launchctl bootout system ${service_file}

Disable auto start:
  sudo launchctl disable system/com.nodekeep.agent

Uninstall:
  sudo launchctl bootout system ${service_file}
  sudo rm -f ${service_file}
  sudo rm -rf ${install_dir}
EOF
        ;;
    esac
}

case "${release_os}" in
linux)
    activate_install
    print_success_instructions
    ;;
darwin)
    activate_install
    print_success_instructions
    ;;
esac
