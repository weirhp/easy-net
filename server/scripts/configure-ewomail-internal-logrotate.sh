#!/usr/bin/env bash
set -euo pipefail

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo "请使用 root 运行。" >&2
  exit 1
fi

container_name="${1:-ewomail}"
clear_existing="${2:-}"
if [[ -n "$clear_existing" && "$clear_existing" != "--clear" ]]; then
  echo "用法: $0 [CONTAINER_NAME] [--clear]" >&2
  exit 2
fi

if [[ ! "$container_name" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*$ ]]; then
  echo "容器名称包含不支持的字符: $container_name" >&2
  exit 2
fi

if ! docker inspect "$container_name" >/dev/null 2>&1; then
  echo "容器不存在: $container_name" >&2
  exit 3
fi

if [[ "$(docker inspect "$container_name" --format '{{.State.Running}}')" != "true" ]]; then
  echo "容器未运行: $container_name" >&2
  exit 3
fi

if ! docker exec "$container_name" test -x /usr/sbin/logrotate; then
  echo "容器内缺少 /usr/sbin/logrotate: $container_name" >&2
  exit 4
fi

log_paths=(
  /var/log/dovecot.log
  /var/log/maillog
  /var/log/messages
)

for log_path in "${log_paths[@]}"; do
  if ! docker exec "$container_name" test -f "$log_path"; then
    echo "拒绝继续，目标不是普通文件或不存在: $log_path" >&2
    exit 4
  fi
done

safe_name="${container_name//./_}"
config_dir="/etc/easy-net"
host_rule="$config_dir/ewomail-internal-${safe_name}.conf"
hourly_path="/etc/cron.hourly/ewomail-internal-${safe_name}"
container_rule="/etc/easy-net/internal-logs.conf"
container_state="/var/lib/easy-net-internal-logrotate.status"

install -d -m 0755 "$config_dir"
tee "$host_rule" >/dev/null <<'EOF'
/var/log/dovecot.log {
    size 50M
    rotate 3
    compress
    copytruncate
    missingok
    notifempty
}

/var/log/maillog {
    size 50M
    rotate 3
    compress
    copytruncate
    missingok
    notifempty
}

/var/log/messages {
    size 50M
    rotate 3
    compress
    copytruncate
    missingok
    notifempty
}
EOF
chmod 0644 "$host_rule"

tee "$hourly_path" >/dev/null <<EOF
#!/usr/bin/env bash
set -euo pipefail

container_name="$container_name"
host_rule="$host_rule"
container_rule="$container_rule"
container_state="$container_state"

if [[ "\$(docker inspect "\$container_name" --format '{{.State.Running}}' 2>/dev/null || true)" != "true" ]]; then
  exit 0
fi

docker exec "\$container_name" mkdir -p /etc/easy-net
docker cp "\$host_rule" "\$container_name:\$container_rule" >/dev/null
docker exec "\$container_name" chmod 0644 "\$container_rule"
docker exec "\$container_name" /usr/sbin/logrotate -s "\$container_state" "\$container_rule"
EOF
chmod 0755 "$hourly_path"

validation_rule="$(mktemp)"
trap 'rm -f "$validation_rule"' EXIT
sed 's/size 50M/size 100G/' "$host_rule" >"$validation_rule"
docker exec "$container_name" mkdir -p /etc/easy-net
docker exec "$container_name" rm -f /etc/logrotate.d/easy-net-internal-logs
docker cp "$host_rule" "$container_name:$container_rule" >/dev/null
docker exec "$container_name" chmod 0644 "$container_rule"
docker cp "$validation_rule" "$container_name:/etc/easy-net/internal-logs.validate.conf" >/dev/null
docker exec "$container_name" /usr/sbin/logrotate -d -s /dev/null /etc/easy-net/internal-logs.validate.conf >/dev/null 2>&1
docker exec "$container_name" rm -f /etc/easy-net/internal-logs.validate.conf
rm -f "$validation_rule"
trap - EXIT

if [[ "$clear_existing" == "--clear" ]]; then
  docker exec "$container_name" truncate -s 0 "${log_paths[@]}"
  echo "已清空以下现有日志（不可恢复）:"
  printf '  %s\n' "${log_paths[@]}"
fi

"$hourly_path"

echo "已为 $container_name 配置内部日志每小时检查：每个文件 50 MiB，保留 3 份压缩历史。"
echo "宿主机规则: $host_rule"
echo "宿主机任务: $hourly_path"
echo "容器内规则: $container_rule"
