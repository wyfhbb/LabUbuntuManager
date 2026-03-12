#!/bin/bash

# 深度学习服务器 - 磁盘用量查询工具
# 此脚本允许任何用户查询当前所有用户的磁盘使用情况

# 配置
CURRENT_USAGE_FILE="/var/log/disk-usage/current-usage.txt"

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
NC='\033[0m' # 无颜色

# 帮助信息
show_help() {
    echo -e "${BLUE}深度学习服务器磁盘用量查询工具${NC}"
    echo "用法: $(basename "$0") [选项]"
    echo ""
    echo "选项:"
    echo "  -h, --help         显示此帮助信息"
    echo "  -s, --sort <列>    按指定列排序 (user, home, workspace, total)"
    echo "  -r, --reverse      反向排序"
    echo "  -m, --me           只显示当前用户的使用情况"
    echo "  -n, --no-color     不使用颜色输出"
    echo ""
    echo "示例:"
    echo "  $(basename "$0")              # 显示所有用户磁盘使用情况"
    echo "  $(basename "$0") --sort total # 按总使用量排序"
    echo "  $(basename "$0") -m           # 只显示我的使用情况"
    echo "数据每日更新"
}

# 参数解析
SORT_COLUMN="user"
REVERSE_SORT=0
SHOW_ONLY_ME=0
USE_COLOR=1

while (( "$#" )); do
    case "$1" in
        -h|--help)
            show_help
            exit 0
            ;;
        -s|--sort)
            if [ -n "$2" ] && [ ${2:0:1} != "-" ]; then
                case "$2" in
                    user|home|workspace|total)
                        SORT_COLUMN=$2
                        shift 2
                        ;;
                    *)
                        echo -e "${RED}错误: 不支持的排序列 '$2'${NC}" >&2
                        exit 1
                        ;;
                esac
            else
                echo -e "${RED}错误: --sort 选项需要一个参数${NC}" >&2
                exit 1
            fi
            ;;
        -r|--reverse)
            REVERSE_SORT=1
            shift
            ;;
        -m|--me)
            SHOW_ONLY_ME=1
            shift
            ;;
        -n|--no-color)
            USE_COLOR=0
            shift
            ;;	
        -*|--*)
            echo -e "${RED}错误: 未知选项 $1${NC}" >&2
            exit 1
            ;;
        *)
            shift
            ;;
    esac
done

# 检查文件是否存在
if [ ! -f "$CURRENT_USAGE_FILE" ]; then
    echo -e "${RED}错误: 磁盘使用报告文件不存在${NC}" >&2
    echo "请联系系统管理员运行磁盘使用统计脚本" >&2
    exit 1
fi

# 不再使用HTML报告

# 获取当前用户
CURRENT_USER=$(whoami)

# 读取数据
HEADER=$(head -n 5 "$CURRENT_USAGE_FILE")
DIVIDER=$(head -n 6 "$CURRENT_USAGE_FILE" | tail -n 1)
DATA=$(tail -n +7 "$CURRENT_USAGE_FILE")

# 如果只显示当前用户
if [ $SHOW_ONLY_ME -eq 1 ]; then
    DATA=$(echo "$DATA" | grep "^$CURRENT_USER ")
    if [ -z "$DATA" ]; then
        echo -e "${RED}未找到当前用户 $CURRENT_USER 的使用数据${NC}" >&2
        exit 1
    fi
fi

# 排序数据
case "$SORT_COLUMN" in
    user)
        COL=1
        ;;
    home)
        COL=2
        ;;
    workspace)
        COL=3
        ;;
    total)
        COL=4
        ;;
    *)
        COL=1
        ;;
esac

if [ $REVERSE_SORT -eq 1 ]; then
    SORT_OPT=""
else
    SORT_OPT="-r"
fi

# 处理排序 (去除GB并按数值排序)
if [ $COL -ne 1 ]; then
    SORTED_DATA=$(echo "$DATA" | awk -v col="$COL" '{
        val = $col;
        gsub("GB", "", val);
        print val "@" $0;
    }' | sort $SORT_OPT -n | cut -d '@' -f2-)
else
    SORTED_DATA=$(echo "$DATA" | sort $SORT_OPT -k1,1)
fi

# 显示表头和数据
if [ $USE_COLOR -eq 1 ]; then
    echo -e "${YELLOW}$HEADER${NC}"
    echo -e "${YELLOW}$DIVIDER${NC}"
    
    # 着色显示每一行
    echo "$SORTED_DATA" | while read -r line; do
        USER=$(echo "$line" | awk '{print $1}')
        
        if [ "$USER" = "$CURRENT_USER" ]; then
            # 高亮显示当前用户
            echo -e "${GREEN}$line${NC}"
        else
            echo "$line"
        fi
    done
else
    echo "$HEADER"
    echo "$DIVIDER"
    echo "$SORTED_DATA"
fi

echo "统计时间: $(stat -c %y "$CURRENT_USAGE_FILE" | cut -d. -f1)"

exit 0