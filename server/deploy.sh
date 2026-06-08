#!/bin/bash

# 切换到脚本所在目录，确保 docker compose 能读取同目录下的 docker-compose.yml 和 .env。
cd "$(dirname "$0")" || exit 1

# 获取 Docker Compose 执行基础命令
get_docker_compose_base() {
    if docker compose version &> /dev/null; then
        echo "docker compose"
    elif command -v docker-compose &> /dev/null; then
        echo "docker-compose"
    else
        echo ""
    fi
}

BASE_CMD=$(get_docker_compose_base)
if [ -z "$BASE_CMD" ]; then
    echo "错误: 未检测到 docker compose 或 docker-compose，请先安装 Docker 环境。"
    exit 1
fi

# 打印帮助信息
print_help() {
    echo "使用说明:"
    echo "  $0 [command] [options]"
    echo ""
    echo "支持的命令 (command):"
    echo "  start         构建并启动服务 (默认)"
    echo "  stop          停止服务"
    echo "  restart       重启服务"
    echo "  logs          查看实时运行日志"
    echo "  status        查看服务状态"
    echo ""
    echo "可选参数 (options):"
    echo "  -p [project]  指定 Docker Compose 项目名称 (Project Name)"
    echo "  --port [port] 指定宿主机映射端口 (默认: 使用 .env 中的 HOST_PORT，其次为 3100)"
    echo "  -h, --help    显示帮助信息"
    echo ""
    echo "示例:"
    echo "  $0 start -p my-proxy                (指定项目名为 my-proxy 启动)"
    echo "  $0 start -p group1 --port 8080      (指定项目名且映射到 8080 端口)"
    echo "  $0 stop -p my-proxy                 (停止指定项目名的服务)"
    echo "  $0 logs -p my-proxy                 (查看指定项目的实时日志)"
}

# 默认参数
COMMAND="start"
PROJECT_NAME=""
CUSTOM_PORT=""

# 手动解析命令行参数，支持命令和选项的任意顺序组合
while [[ $# -gt 0 ]]; do
    case "$1" in
        start|stop|restart|logs|status)
            COMMAND="$1"
            shift
            ;;
        -p)
            if [[ -n "$2" && ! "$2" =~ ^- ]]; then
                PROJECT_NAME="$2"
                shift 2
            else
                echo "错误: -p 参数需要指定项目名称"
                exit 1
            fi
            ;;
        --port)
            if [[ -n "$2" && ! "$2" =~ ^- ]]; then
                CUSTOM_PORT="$2"
                shift 2
            else
                echo "错误: --port 参数需要指定端口号"
                exit 1
            fi
            ;;
        -h|--help)
            print_help
            exit 0
            ;;
        *)
            echo "未知参数: $1"
            print_help
            exit 1
            ;;
    esac
done

# 拼装带有项目名称的完整 compose 命令
COMPOSE_CMD="$BASE_CMD"
if [ -n "$PROJECT_NAME" ]; then
    COMPOSE_CMD="$COMPOSE_CMD -p $PROJECT_NAME"
    echo "已指定 Docker Compose 项目名称: $PROJECT_NAME"
fi

# 处理端口映射。未传 --port 时不要导出 HOST_PORT，否则会覆盖 compose 自动读取的 .env。
if [ -n "$CUSTOM_PORT" ]; then
    export HOST_PORT="$CUSTOM_PORT"
    echo "已指定外部端口: $HOST_PORT"
else
    if [ -n "$HOST_PORT" ]; then
        echo "使用当前环境变量 HOST_PORT: $HOST_PORT"
    elif [ -f ".env" ] && grep -Eq '^[[:space:]]*HOST_PORT=' ".env"; then
        DISPLAY_PORT=$(grep -E '^[[:space:]]*HOST_PORT=' ".env" | tail -n 1 | cut -d '=' -f 2- | tr -d '[:space:]"')
        echo "使用 .env 中的外部端口: $DISPLAY_PORT"
    else
        echo "未指定外部端口，将使用 docker-compose.yml 默认端口: 3100"
    fi
fi

case "$COMMAND" in
    start)
        echo "正在构建并运行服务..."
        $COMPOSE_CMD up -d --build
        echo "================================================="
        echo "服务已成功启动！"
        echo "================================================="
        ;;
    stop)
        echo "正在停止服务..."
        $COMPOSE_CMD down
        echo "服务已停止。"
        ;;
    restart)
        echo "正在重启服务..."
        $COMPOSE_CMD restart
        ;;
    logs)
        $COMPOSE_CMD logs -f
        ;;
    status)
        $COMPOSE_CMD ps
        ;;
esac
