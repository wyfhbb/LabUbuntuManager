#!/bin/bash
LOG_DIR="/var/log/disk-usage"
LOG_FILE="$LOG_DIR/disk-usage-$(date +%Y-%m-%d).log"
REPORT_FILE="$LOG_DIR/current-usage.txt"
MAX_LOGS=30  # 保留最近30天的日志

mkdir -p "$LOG_DIR"

echo "=====================================================" > "$LOG_FILE"
echo "       深度学习服务器 - 磁盘用量统计报告" >> "$LOG_FILE"
echo "       生成时间: $(date '+%Y-%m-%d %H:%M:%S')" >> "$LOG_FILE"
echo "=====================================================" >> "$LOG_FILE"
echo "" >> "$LOG_FILE"

echo "系统磁盘使用情况:" >> "$LOG_FILE"
df -h | grep -E '/$|/home|/workspace' >> "$LOG_FILE"
echo "" >> "$LOG_FILE"

# 获取所有用户列表 (排除软件用户)
USERS=$(awk -F':' '{if ($3 >= 1000 && $3 != 65534) print $1}' /etc/passwd)
echo "用户磁盘使用详情:" >> "$LOG_FILE"
printf "%-15s %-15s %-15s %-15s %-20s\n" "user" "home" "workspace" "total" "full_name" >> "$LOG_FILE"
echo "---------------------------------------------------------------------------------" >> "$LOG_FILE"
echo "=====================================================" > "$REPORT_FILE"
echo "       深度学习服务器 - 当前磁盘用量报告" > "$REPORT_FILE"
echo "       生成时间: $(date '+%Y-%m-%d %H:%M:%S')" >> "$REPORT_FILE"
echo "=====================================================" >> "$REPORT_FILE"
echo "" >> "$REPORT_FILE"
printf "%-15s %-15s %-15s %-15s %-20s\n" "user" "home" "workspace" "total" "full_name" >> "$REPORT_FILE"
echo "---------------------------------------------------------------------------------" >> "$REPORT_FILE"
# 遍历每个用户并计算磁盘使用情况
for USER in $USERS; do
    # 获取用户的真实名称
    FULL_NAME=$(getent passwd "$USER" | cut -d: -f5 | cut -d, -f1)
    
    # 检查用户是否有对应目录
    HOME_DIR="/home/$USER"
    WORKSPACE_DIR="/workspace/$USER"
    
    # 计算各目录使用量（以GB为单位）
    if [ -d "$HOME_DIR" ]; then
        # 排除工作空间的符号链接，只统计实际的主目录内容
        HOME_USAGE=$(du -sk "$HOME_DIR" --exclude="$HOME_DIR/workspace" 2>/dev/null | awk '{printf "%.2f", $1/1024/1024}')
    else
        HOME_USAGE="0.00"
    fi
    if [ -d "$WORKSPACE_DIR" ]; then
        WORKSPACE_USAGE=$(du -sk "$WORKSPACE_DIR" 2>/dev/null | awk '{printf "%.2f", $1/1024/1024}')
    else
        WORKSPACE_USAGE="0.00"
    fi
    
    # 计算总量
    TOTAL_USAGE=$(awk "BEGIN {printf \"%.2f\", $HOME_USAGE + $WORKSPACE_USAGE}")
    printf "%-15s %-15s %-15s %-15s %-20s\n" "$USER" "${HOME_USAGE}GB" "${WORKSPACE_USAGE}GB" "${TOTAL_USAGE}GB" "$FULL_NAME" >> "$LOG_FILE"
    printf "%-15s %-15s %-15s %-15s %-20s\n" "$USER" "${HOME_USAGE}GB" "${WORKSPACE_USAGE}GB" "${TOTAL_USAGE}GB" "$FULL_NAME" >> "$REPORT_FILE"
done

find "$LOG_DIR" -name "disk-usage-*.log" -type f | sort -r | tail -n +$((MAX_LOGS+1)) | xargs -r rm

echo "" >> "$LOG_FILE"
echo "统计完成，报告已保存到 $REPORT_FILE" >> "$LOG_FILE"
echo "脚本执行完毕: $(date '+%Y-%m-%d %H:%M:%S')" >> "$LOG_FILE"


chmod 644 "$REPORT_FILE"
chmod 644 "$LOG_FILE"

exit 0