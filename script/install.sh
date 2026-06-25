#!/bin/bash

#========================================================
#   System Required: CentOS 7+ / Debian 8+ / Ubuntu 16+ /
#     Arch 未测试
#   Description: nodekeep安装脚本
#   Github: https://github.com/r0n9/nodekeep
#========================================================

BASE_PATH="/opt/nodekeep"
DASHBOARD_PATH="${BASE_PATH}/dashboard"
AGENT_PATH="${BASE_PATH}/agent"
AGENT_SERVICE="/etc/systemd/system/nodekeep-agent.service"
VERSION="v2.0.1"

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'
export PATH=$PATH:/usr/local/bin

os_arch=""

pre_check() {
    command -v systemctl >/dev/null 2>&1
    if [[ $? != 0 ]]; then
        echo "不支持此系统：未找到 systemctl 命令"
        exit 1
    fi

    # check root
    [[ $EUID -ne 0 ]] && echo -e "${red}错误: ${plain} 必须使用root用户运行此脚本！\n" && exit 1

    ## os_arch
    if [[ $(uname -m | grep 'x86_64') != "" ]]; then
        os_arch="amd64"
    elif [[ $(uname -m | grep 'i386\|i686') != "" ]]; then
        os_arch="386"
    elif [[ $(uname -m | grep 'aarch64\|armv8b\|armv8l') != "" ]]; then
        os_arch="arm64"
    elif [[ $(uname -m | grep 'arm') != "" ]]; then
        os_arch="arm"
    fi

    if [[ -z "${CN}" ]]; then
        GITHUB_RAW_URL="raw.githubusercontent.com"
        GITHUB_URL="github.com"
    else
        GITHUB_RAW_URL="raw.sevencdn.com"
        GITHUB_URL="hub.fastgit.org"
    fi
}

confirm() {
    if [[ $# > 1 ]]; then
        echo && read -p "$1 [默认$2]: " temp
        if [[ x"${temp}" == x"" ]]; then
            temp=$2
        fi
    else
        read -p "$1 [y/n]: " temp
    fi
    if [[ x"${temp}" == x"y" || x"${temp}" == x"Y" ]]; then
        return 0
    else
        return 1
    fi
}

before_show_menu() {
    echo && echo -n -e "${yellow}* 按回车返回主菜单 *${plain}" && read temp
    show_menu
}

install_base() {
    (command -v curl >/dev/null 2>&1 && command -v wget >/dev/null 2>&1 && command -v tar >/dev/null 2>&1) ||
        (install_soft curl wget tar)
}

install_soft() {
    (command -v yum >/dev/null 2>&1 && yum install $* -y) ||
        (command -v apt >/dev/null 2>&1 && apt install $* -y) ||
        (command -v pacman >/dev/null 2>&1 && pacman -Syu $*) ||
        (command -v apt-get >/dev/null 2>&1 && apt-get install $* -y)
}

download_file() {
    local url="$1"
    local output="$2"

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$output"
    else
        wget -O "$output" "$url"
    fi
}

compose() {
    if docker compose version >/dev/null 2>&1; then
        docker compose "$@"
    else
        docker-compose "$@"
    fi
}

replace_placeholder() {
    local placeholder="$1"
    local value="$2"
    local file="$3"
    local escaped

    escaped=$(printf '%s' "$value" | sed -e 's/[\/&|\\]/\\&/g')
    sed -i "s|${placeholder}|${escaped}|g" "$file"
}

yaml_quote() {
    local value="$1"

    value=${value//\\/\\\\}
    value=${value//\"/\\\"}
    value=${value//$'\n'/ }
    printf '"%s"' "$value"
}

write_dashboard_config() {
    local file="$1"

    cat > "$file" <<EOF
debug: false
httpport: 8008
auth:
  local:
    enabled: true
    username: $(yaml_quote "${local_admin_username}")
    password: $(yaml_quote "${local_admin_password}")
agent:
  install_host: $(yaml_quote "${agent_install_host}")
  tls: ${agent_tls}
oauth2:
  type: $(yaml_quote "${oauth2_type}")
  admin: $(yaml_quote "${admin_logins}")
  clientid: $(yaml_quote "${github_oauth_client_id}")
  clientsecret: $(yaml_quote "${github_oauth_client_secret}")
site:
  brand: $(yaml_quote "${site_title}")
  cookiename: "nodekeep-dashboard"
EOF
}

install_dashboard() {
    install_base

    echo -e "> 安装面板"

    # nodekeep文件夹
    mkdir -p $DASHBOARD_PATH
    chmod 755 -R $DASHBOARD_PATH

    command -v docker >/dev/null 2>&1
    if [[ $? != 0 ]]; then
        echo -e "正在安装 Docker"
        bash <(curl -sL https://get.docker.com) >/dev/null 2>&1
        if [[ $? != 0 ]]; then
            echo -e "${red}下载脚本失败，请检查本机能否连接 get.docker.com${plain}"
            return 0
        fi
        systemctl enable docker.service
        systemctl start docker.service
        echo -e "${green}Docker${plain} 安装成功"
    fi

    if ! docker compose version >/dev/null 2>&1 && ! command -v docker-compose >/dev/null 2>&1; then
        compose_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
        compose_arch="$(uname -m)"
        echo -e "正在安装 Docker Compose"
        download_file "https://${GITHUB_URL}/docker/compose/releases/latest/download/docker-compose-${compose_os}-${compose_arch}" /usr/local/bin/docker-compose >/dev/null 2>&1
        if [[ $? != 0 ]]; then
            echo -e "${red}下载脚本失败，请检查本机能否连接 ${GITHUB_URL}${plain}"
            return 0
        fi
        chmod +x /usr/local/bin/docker-compose
        echo -e "${green}Docker Compose${plain} 安装成功"
    fi

    modify_dashboard_config 0

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

install_agent() {
    install_base

    echo -e "> 安装监测Agent"
    run_agent_installer

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

modify_agent_config() {
    echo -e "> 修改Agent配置"
    install_base
    run_agent_installer

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

run_agent_installer() {
    local installer="/tmp/nodekeep-install-agent.sh"
    local dashboard_server
    local dashboard_tls
    local client_secret
    local agent_args

    echo -e "正在下载 Agent 安装脚本"
    download_file "https://${GITHUB_RAW_URL}/r0n9/nodekeep/master/script/install-agent.sh" "$installer" >/dev/null 2>&1
    if [[ $? != 0 ]]; then
        echo -e "${red}文件下载失败，请检查本机能否连接 ${GITHUB_RAW_URL}${plain}"
        return 0
    fi
    chmod +x "$installer"

    echo "请先在管理面板上添加Agent，记录下密钥" &&
        read -p "请输入 Dashboard 接入地址（例如 nodekeep.example.com:443 或 127.0.0.1:8008）: " dashboard_server &&
        read -p "Agent 是否使用 TLS 连接？HTTPS 反代填 y，直连 HTTP 填 N (y/N): " dashboard_tls &&
        read -p "请输入Agent 密钥: " client_secret
    if [[ -z "${dashboard_server}" || -z "${client_secret}" ]]; then
        echo -e "${red}所有选项都不能为空${plain}"
        before_show_menu
        return 1
    fi

    agent_args=("-s" "${dashboard_server}" "-p" "${client_secret}")
    if [[ ! "${dashboard_tls}" =~ ^[Yy]$ ]]; then
        agent_args+=("--insecure")
    fi

    bash "$installer" "${agent_args[@]}"
}

modify_dashboard_config() {
    echo -e "> 修改面板配置"

    echo -e "正在下载 Docker 脚本"
    download_file "https://${GITHUB_RAW_URL}/r0n9/nodekeep/master/script/docker-compose.yaml" "${DASHBOARD_PATH}/docker-compose.yaml" >/dev/null 2>&1
    if [[ $? != 0 ]]; then
        echo -e "${red}下载脚本失败，请检查本机能否连接 ${GITHUB_RAW_URL}${plain}"
        return 0
    fi

    mkdir -p $DASHBOARD_PATH/data

    echo "本地管理员账号用于首次部署登录，请登录后尽快在设置页修改密码。" &&
        read -p "请输入本地管理员用户名: (admin)" local_admin_username &&
        read -p "请输入本地管理员密码: " local_admin_password &&
        echo "OAuth2 为可选登录方式，可留空跳过。" &&
        echo "关于 GitHub Oauth2 应用：在 https://github.com/settings/developers 创建，无需审核，Callback 填 http(s)://域名或IP/oauth2/callback" &&
        echo "关于 Gitee Oauth2 应用：在 https://gitee.com/oauth/applications 创建，无需审核，Callback 填 http(s)://域名或IP/oauth2/callback" &&
        read -p "请输入 OAuth2 提供商(gitee/github，默认 github): " oauth2_type &&
        read -p "请输入 Oauth2 应用的 Client ID: " github_oauth_client_id &&
        read -p "请输入 Oauth2 应用的 Client Secret: " github_oauth_client_secret &&
        read -p "请输入 GitHub/Gitee 登录名作为管理员，多个以逗号隔开: " admin_logins &&
        read -p "请输入站点标题: " site_title &&
        read -p "请输入站点访问和 Agent 接入端口: (8008)" site_port &&
        read -p "请输入 Agent 安装命令中的面板连接地址（例如 example.com:443）: " agent_install_host &&
        read -p "Agent 是否使用 TLS 连接？(y/N): " agent_tls_input

    if [[ -z "${local_admin_password}" || -z "${site_title}" || -z "${agent_install_host}" ]]; then
        echo -e "${red}所有选项都不能为空${plain}"
        before_show_menu
        return 1
    fi

    if [[ -z "${local_admin_username}" ]]; then
        local_admin_username=admin
    fi
    if [[ -z "${site_port}" ]]; then
        site_port=8008
    fi
    if [[ -z "${oauth2_type}" ]]; then
        oauth2_type=github
    fi
    agent_tls=false
    if [[ "${agent_tls_input}" =~ ^[Yy]$ ]]; then
        agent_tls=true
    fi

    write_dashboard_config "${DASHBOARD_PATH}/data/config.yaml"
    replace_placeholder "site_port" "${site_port}" "${DASHBOARD_PATH}/docker-compose.yaml"

    echo -e "面板配置 ${green}修改成功，请稍等重启生效${plain}"

    restart_and_update

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

restart_and_update() {
    echo -e "> 重启并更新面板"

    cd $DASHBOARD_PATH
    compose pull
    compose down
    compose up -d
    if [[ $? == 0 ]]; then
        echo -e "${green}nodekeep 重启成功${plain}"
        echo -e "默认管理面板地址：${yellow}域名:站点访问端口${plain}"
    else
        echo -e "${red}重启失败，可能是因为启动时间超过了两秒，请稍后查看日志信息${plain}"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

start_dashboard() {
    echo -e "> 启动面板"

    cd $DASHBOARD_PATH && compose up -d
    if [[ $? == 0 ]]; then
        echo -e "${green}nodekeep 启动成功${plain}"
    else
        echo -e "${red}启动失败，请稍后查看日志信息${plain}"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

stop_dashboard() {
    echo -e "> 停止面板"

    cd $DASHBOARD_PATH && compose down
    if [[ $? == 0 ]]; then
        echo -e "${green}nodekeep 停止成功${plain}"
    else
        echo -e "${red}停止失败，请稍后查看日志信息${plain}"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

show_dashboard_log() {
    echo -e "> 获取面板日志"

    cd $DASHBOARD_PATH && compose logs -f

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

uninstall_dashboard() {
    echo -e "> 卸载管理面板"

    cd $DASHBOARD_PATH &&
        compose down
    rm -rf $DASHBOARD_PATH
    clean_all

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

show_agent_log() {
    echo -e "> 获取Agent日志"

    journalctl -xf -u nodekeep-agent.service

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

uninstall_agent() {
    echo -e "> 卸载Agent"

    systemctl disable nodekeep-agent.service
    systemctl stop nodekeep-agent.service
    rm -rf $AGENT_SERVICE
    systemctl daemon-reload

    rm -rf $AGENT_PATH
    clean_all

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

restart_agent() {
    echo -e "> 重启Agent"

    systemctl restart nodekeep-agent.service

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

clean_all() {
    if [ -z "$(ls -A ${BASE_PATH})" ]; then
        rm -rf ${BASE_PATH}
    fi
}

show_usage() {
    echo "nodekeep 管理脚本使用方法: "
    echo "--------------------------------------------------------"
    echo "./nodekeep.sh                            - 显示管理菜单"
    echo "./nodekeep.sh install_dashboard          - 安装面板端"
    echo "./nodekeep.sh modify_dashboard_config    - 修改面板配置"
    echo "./nodekeep.sh start_dashboard            - 启动面板"
    echo "./nodekeep.sh stop_dashboard             - 停止面板"
    echo "./nodekeep.sh restart_and_update         - 重启并更新面板"
    echo "./nodekeep.sh show_dashboard_log         - 查看面板日志"
    echo "./nodekeep.sh uninstall_dashboard        - 卸载管理面板"
    echo "--------------------------------------------------------"
    echo "./nodekeep.sh install_agent              - 安装监测Agent"
    echo "./nodekeep.sh modify_agent_config        - 修改Agent配置"
    echo "./nodekeep.sh show_agent_log             - 查看Agent日志"
    echo "./nodekeep.sh uninstall_agent            - 卸载Agen"
    echo "./nodekeep.sh restart_agent              - 重启Agen"
    echo "--------------------------------------------------------"
}

show_menu() {
    echo -e "
    ${green}nodekeep管理脚本${plain} ${red}${VERSION}${plain}
    --- https://github.com/r0n9/nodekeep ---
    ${green}0.${plain}  退出脚本
    ————————————————-
    ${green}1.${plain}  安装面板端
    ${green}2.${plain}  修改面板配置
    ${green}3.${plain}  启动面板
    ${green}4.${plain}  停止面板
    ${green}5.${plain}  重启并更新面板
    ${green}6.${plain}  查看面板日志
    ${green}7.${plain}  卸载管理面板
    ————————————————-
    ${green}8.${plain}  安装监测Agent
    ${green}9.${plain}  修改Agent配置
    ${green}10.${plain} 查看Agent日志
    ${green}11.${plain} 卸载Agent
    ${green}12.${plain} 重启Agent
    "
    echo && read -p "请输入选择 [0-12]: " num

    case "${num}" in
    0)
        exit 0
        ;;
    1)
        install_dashboard
        ;;
    2)
        modify_dashboard_config
        ;;
    3)
        start_dashboard
        ;;
    4)
        stop_dashboard
        ;;
    5)
        restart_and_update
        ;;
    6)
        show_dashboard_log
        ;;
    7)
        uninstall_dashboard
        ;;
    8)
        install_agent
        ;;
    9)
        modify_agent_config
        ;;
    10)
        show_agent_log
        ;;
    11)
        uninstall_agent
        ;;
    12)
        restart_agent
        ;;
    *)
        echo -e "${red}请输入正确的数字 [0-12]${plain}"
        ;;
    esac
}

pre_check

if [[ $# > 0 ]]; then
    case $1 in
    "install_dashboard")
        install_dashboard 0
        ;;
    "modify_dashboard_config")
        modify_dashboard_config 0
        ;;
    "start_dashboard")
        start_dashboard 0
        ;;
    "stop_dashboard")
        stop_dashboard 0
        ;;
    "restart_and_update")
        restart_and_update 0
        ;;
    "show_dashboard_log")
        show_dashboard_log 0
        ;;
    "uninstall_dashboard")
        uninstall_dashboard 0
        ;;
    "install_agent")
        install_agent 0
        ;;
    "modify_agent_config")
        modify_agent_config 0
        ;;
    "show_agent_log")
        show_agent_log 0
        ;;
    "uninstall_agent")
        uninstall_agent 0
        ;;
    "restart_agent")
        restart_agent 0
        ;;
    *) show_usage ;;
    esac
else
    show_menu
fi
