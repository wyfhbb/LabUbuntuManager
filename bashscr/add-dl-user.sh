#!/bin/bash

# 交互式深度学习服务器用户添加脚本
# 添加用户并在机械硬盘上创建相应的工作空间和归档目录

# 检查是否以root权限运行
if [ "$(id -u)" -ne 0 ]; then
    echo "错误: 请使用root权限运行此脚本" >&2
    echo "请尝试: sudo $0" >&2
    exit 1
fi

# 欢迎信息
clear
echo "================================================"
echo "       深度学习服务器 - 用户添加工具"
echo "================================================"
echo "此工具将创建新用户并配置存储目录映射:"
echo "- 在用户主目录创建符号链接到专用存储区域"
echo "- 创建 /workspace/<用户名> 目录用于工作空间"
echo "================================================"
echo ""

# 交互获取用户信息
read -p "请输入用户名: " USERNAME
while [[ -z "$USERNAME" ]]; do
    read -p "用户名不能为空，请重新输入: " USERNAME
done

# 检查用户名是否已存在
if id "$USERNAME" &>/dev/null; then
    echo "错误: 用户 '$USERNAME' 已存在"
    exit 1
fi

# 检查用户名合法性（仅允许小写字母、数字和下划线）
if ! [[ $USERNAME =~ ^[a-z0-9_]+$ ]]; then
    echo "错误: 用户名只能包含小写字母、数字和下划线"
    exit 1
fi

read -p "请输入用户全名: " FULLNAME
while [[ -z "$FULLNAME" ]]; do
    read -p "用户全名不能为空，请重新输入: " FULLNAME
done

# 获取密码（不显示输入内容）
read -s -p "请输入用户密码: " PASSWORD
echo ""
while [[ -z "$PASSWORD" ]]; do
    read -s -p "密码不能为空，请重新输入: " PASSWORD
    echo ""
done

read -s -p "请再次输入密码确认: " PASSWORD_CONFIRM
echo ""
while [[ "$PASSWORD" != "$PASSWORD_CONFIRM" ]]; do
    echo "密码不匹配，请重新输入"
    read -s -p "请输入用户密码: " PASSWORD
    echo ""
    read -s -p "请再次输入密码确认: " PASSWORD_CONFIRM
    echo ""
done

# 工作空间和归档目录的路径
WORKSPACE_DIR="/workspace/$USERNAME"
HOME_DIR="/home/$USERNAME"

# 检查机械硬盘挂载点是否存在
if [ ! -d "/workspace" ] ; then
    echo "错误: /workspace 目录不存在，请确保硬盘已正确挂载"
    exit 1
fi

# 确认信息
echo ""
echo "==== 请确认以下信息 ===="
echo "用户名: $USERNAME"
echo "全名: $FULLNAME"
echo "工作空间: $WORKSPACE_DIR -> $HOME_DIR/workspace"
echo "========================="
read -p "确认创建用户? (y/n): " CONFIRM

if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
    echo "操作已取消"
    exit 0
fi

# 创建用户
echo -n "正在创建用户... "
useradd -m -c "$FULLNAME" -s /bin/bash "$USERNAME"
echo "$USERNAME:$PASSWORD" | chpasswd
echo "完成"

# 创建工作空间和归档目录
echo -n "正在创建用户专属目录... "
mkdir -p "$WORKSPACE_DIR"

# 设置目录所有权
chown "$USERNAME:$USERNAME" "$WORKSPACE_DIR"

# 设置目录权限 (只有用户本人可以访问)
chmod 700 "$WORKSPACE_DIR"
echo "完成"

# 在用户主目录创建符号链接
echo -n "正在创建符号链接... "
ln -s "$WORKSPACE_DIR" "$HOME_DIR/workspace"

# 确保链接的所有权正确
chown -h "$USERNAME:$USERNAME" "$HOME_DIR/workspace"
echo "完成"

# 给docker运行权限
bash ./docker2user.sh

# 打印用户信息摘要
echo ""
echo "========================================"
echo "✅ 用户创建成功!"
echo "========================================"
echo "用户名: $USERNAME"
echo "全名: $FULLNAME"
echo "主目录: $HOME_DIR"
echo "工作目录: $WORKSPACE_DIR -> $HOME_DIR/workspace"
echo "========================================"

