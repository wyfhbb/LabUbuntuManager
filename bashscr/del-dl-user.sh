#!/bin/bash

# 交互式深度学习服务器用户删除脚本
# 删除用户并清理相关的工作空间和归档目录

# 检查是否以root权限运行
if [ "$(id -u)" -ne 0 ]; then
    echo "错误: 请使用root权限运行此脚本" >&2
    echo "请尝试: sudo $0" >&2
    exit 1
fi

# 欢迎信息
clear
echo "================================================"
echo "       深度学习服务器 - 用户删除工具"
echo "================================================"
echo "此工具将删除用户并清理相关存储:"
echo "- 删除用户账户和主目录"
echo "- 删除 /workspace/<用户名> 工作目录"
echo "警告: 此操作不可逆，所有用户数据将被删除"
echo "================================================"
echo ""

# 列出现有用户
echo "系统中的用户列表 (排除系统用户):"
echo "-------------------------------------"
awk -F: '$3 >= 1000 && $3 != 65534 {print $1}' /etc/passwd | sort
echo "-------------------------------------"
echo ""

# 交互获取用户名
read -p "请输入要删除的用户名: " USERNAME
while [[ -z "$USERNAME" ]]; do
    read -p "用户名不能为空，请重新输入: " USERNAME
done

# 检查用户是否存在
if ! id "$USERNAME" &>/dev/null; then
    echo "错误: 用户 '$USERNAME' 不存在"
    exit 1
fi

# 防止删除重要系统用户
if [[ "$USERNAME" == "root" || "$USERNAME" == $(whoami) ]]; then
    echo "错误: 不能删除root用户或当前登录用户"
    exit 1
fi

# 确认用户UID是否 >= 1000 (普通用户)
UID_NUM=$(id -u "$USERNAME")
if [ "$UID_NUM" -lt 1000 ]; then
    echo "错误: 不能删除系统用户 (UID < 1000)"
    exit 1
fi

# 定义相关目录
HOME_DIR=$(eval echo ~$USERNAME)
WORKSPACE_DIR="/workspace/$USERNAME"

# 显示要删除的目录
echo "将删除以下用户和目录:"
echo "- 用户: $USERNAME"
echo "- 主目录: $HOME_DIR"
if [ -d "$WORKSPACE_DIR" ]; then
    echo "- 工作空间: $WORKSPACE_DIR"
    WORKSPACE_SIZE=$(du -sh "$WORKSPACE_DIR" 2>/dev/null | cut -f1)
    echo "  大小: $WORKSPACE_SIZE"
fi

# 要求输入用户名以确认
echo ""
echo "⚠️  警告: 此操作将永久删除所有用户数据，无法恢复"
echo "如需继续，请输入用户名进行确认"
read -p "输入用户名 '$USERNAME' 确认删除: " CONFIRM_USERNAME

if [ "$CONFIRM_USERNAME" != "$USERNAME" ]; then
    echo "确认失败，操作已取消"
    exit 0
fi

# 开始删除过程
echo ""
echo "开始删除用户和相关目录..."

# 先保存当前用户的进程
echo -n "检查用户进程... "
USER_PROCESSES=$(pgrep -u "$USERNAME")
if [ -n "$USER_PROCESSES" ]; then
    echo "发现"
    echo "正在终止用户进程..."
    pkill -9 -u "$USERNAME"
    sleep 1
else
    echo "无进程"
fi

# 删除工作空间目录
if [ -d "$WORKSPACE_DIR" ]; then
    echo -n "删除工作空间目录... "
    rm -rf "$WORKSPACE_DIR"
    echo "完成"
fi

# 删除用户账户和主目录
echo -n "删除用户账户和主目录... "
userdel -r "$USERNAME" 2>/dev/null
if [ $? -ne 0 ]; then
    # 如果自动删除失败，尝试手动删除
    rm -rf "$HOME_DIR"
    userdel "$USERNAME"
fi
echo "完成"

# 验证删除结果
if id "$USERNAME" &>/dev/null; then
    echo "警告: 用户账户删除可能不完全，请手动检查"
else
    echo ""
    echo "================================================"
    echo "✅ 用户 '$USERNAME' 已成功删除"
    echo "================================================"
    
    # 检查目录是否已完全删除
    if [ -d "$HOME_DIR" ]; then
        echo "警告: 主目录 $HOME_DIR 仍然存在"
    fi
    if [ -d "$WORKSPACE_DIR" ]; then
        echo "警告: 工作空间目录 $WORKSPACE_DIR 仍然存在"
    fi
fi
