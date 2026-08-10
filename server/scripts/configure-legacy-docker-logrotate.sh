#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo "请使用 root 运行。" >&2
  exit 1
fi

container_name="${1:-}"
clear_existing="${2:-}"
if [[ -z "$container_name" || ( -n "$clear_existing" && "$clear_existing" != "--clear" ) ]]; then
  echo "用法: $0 CONTAINER_NAME [--clear]" >&2
  exit 2
fi

if ! docker inspect "$container_name" >/dev/null 2>&1; then
  echo "容器不存在: $container_name" >&2
  exit 3
fi

log_path="$(docker inspect "$container_name" --format '{{.LogPath}}')"
case "$log_path" in
  /var/lib/docker/containers/*/*-json.log) ;;
  *)
    echo "拒绝处理非标准 Docker json-file 路径: $log_path" >&2
    exit 4
    ;;
esac

safe_name="$(printf '%s' "$container_name" | tr -cd 'A-Za-z0-9_-')"
if [[ -z "$safe_name" ]]; then
  echo "容器名称不能生成安全的配置文件名。" >&2
  exit 4
fi

rule_path="/etc/logrotate.d/docker-${safe_name}"
hourly_path="/etc/cron.hourly/docker-logrotate-${safe_name}"
state_path="/var/lib/logrotate/docker-${safe_name}.status"

tee "$rule_path" >/dev/null <<EOF
$log_path {
    size 10M
    rotate 3
    compress
    delaycompress
    copytruncate
    missingok
    notifempty
}
EOF
chmod 0644 "$rule_path"

tee "$hourly_path" >/dev/null <<EOF
#!/usr/bin/env bash
/usr/sbin/logrotate -s "$state_path" "$rule_path"
EOF
chmod 0755 "$hourly_path"

/usr/sbin/logrotate -d -s /dev/null "$rule_path" >/dev/null 2>&1

if [[ "$clear_existing" == "--clear" ]]; then
  truncate -s 0 "$log_path"
  echo "已清空现有日志: $log_path"
fi

echo "已为 $container_name 配置每小时检查：10 MiB × 3 个历史文件。"
echo "规则: $rule_path"
echo "任务: $hourly_path"
