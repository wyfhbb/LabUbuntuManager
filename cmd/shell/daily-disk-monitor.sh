#!/bin/bash
# server-mgr 磁盘用量每日统计脚本
# 此文件由 server-mgr disk monitor enable 自动生成，请勿手动编辑
#
# 输出格式（TAB 分隔，每用户每盘一行）：
#   username <TAB> mount_point <TAB> usage_gb <TAB> full_name

LOG_DIR="/var/log/disk-usage"
REPORT_FILE="$LOG_DIR/current-usage.txt"
MAX_LOGS=30

mkdir -p "$LOG_DIR"

# ── 获取数据盘挂载点（过滤掉系统盘，与 Go 端 dataMountCandidates 逻辑一致）──────────

DATA_MOUNTS=$(awk '$1 ~ /^\/dev\// && $3 != "squashfs" { print $2 }' /proc/mounts | while IFS= read -r mp; do
    case "$mp" in
        /|/boot|/boot/efi|/home) continue ;;
        /proc*|/sys*|/dev*|/run*|/tmp*|/snap*) continue ;;
        *) echo "$mp" ;;
    esac
done | sort -u)

# ── 主逻辑 ──────────────────────────────────────────────────────────────────────

TMP=$(mktemp)

{
    echo "# generated: $(date '+%Y-%m-%d %H:%M:%S')"
    echo "# columns: username	disk	usage_gb	full_name"

    while IFS=: read -r uname _ uid _ gecos home_dir shell; do
        # 只统计 UID>=1000 的真实用户，跳过 nobody(65534) 及不可登录账户
        case "$uid" in
            ''|*[!0-9]*) continue ;;
        esac
        [ "$uid" -lt 1000 ] && continue
        [ "$uid" -eq 65534 ] && continue
        [ "$shell" = "/usr/sbin/nologin" ] && continue
        [ "$shell" = "/bin/false" ] && continue
        [ "$shell" = "/sbin/nologin" ] && continue

        full_name=$(echo "$gecos" | cut -d, -f1)

        # home 目录：用 --one-file-system 避免统计到数据盘符号链接指向的内容
        if [ -d "$home_dir" ]; then
            home_kb=$(du -sk --one-file-system "$home_dir" 2>/dev/null | awk '{print $1}')
            home_kb=${home_kb:-0}
            home_gb=$(awk "BEGIN {printf \"%.2f\", $home_kb/1024/1024}")
            printf "%s\t%s\t%s\t%s\n" "$uname" "/home" "$home_gb" "$full_name"
        fi

        # 各数据盘：检查 <mount>/<username> 目录是否存在
        while IFS= read -r mount_point; do
            [ -z "$mount_point" ] && continue
            user_dir="$mount_point/$uname"
            if [ -d "$user_dir" ]; then
                kb=$(du -sk "$user_dir" 2>/dev/null | awk '{print $1}')
                kb=${kb:-0}
                gb=$(awk "BEGIN {printf \"%.2f\", $kb/1024/1024}")
                printf "%s\t%s\t%s\t%s\n" "$uname" "$mount_point" "$gb" "$full_name"
            fi
        done <<< "$DATA_MOUNTS"

    done < /etc/passwd
} > "$TMP"

mv "$TMP" "$REPORT_FILE"
chmod 644 "$REPORT_FILE"

LOG_FILE="$LOG_DIR/disk-usage-$(date +%Y-%m-%d).log"
cp "$REPORT_FILE" "$LOG_FILE"
chmod 644 "$LOG_FILE"

# 只保留最近 MAX_LOGS 天的日志
find "$LOG_DIR" -name "disk-usage-*.log" -type f | sort -r | tail -n +$((MAX_LOGS + 1)) | xargs -r rm

exit 0
