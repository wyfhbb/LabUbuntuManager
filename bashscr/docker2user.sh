#!/bin/bash
#
# 此脚本用于为所有系统（真实）用户授予Docker使用权限
# 实现方法: 将用户添加到docker组

# 确保以root权限运行
if [ "$(id -u)" -ne 0 ]; then
    echo "错误: 请以root用户身份运行此脚本" >&2
    exit 1
fi

# 确认docker组存在
if ! getent group docker > /dev/null; then
    echo "docker组不存在，正在创建..."
    groupadd docker
    if [ $? -ne 0 ]; then
        echo "创建docker组失败" >&2
        exit 1
    fi
    echo "docker组创建成功"
fi

# 获取所有UID大于等于1000的用户（过滤系统用户）
# 排除nologin和false shell的用户
echo "正在获取系统用户列表..."
USERS=$(awk -F: '$3 >= 1000 && $7 != "/usr/sbin/nologin" && $7 != "/bin/false" {print $1}' /etc/passwd)

# 为每个用户添加docker组权限
for USER in $USERS; do
    echo "正在为用户 $USER 添加docker权限..."
    if groups $USER | grep -q '\bdocker\b'; then
        echo "用户 $USER 已经在docker组中"
    else
        usermod -aG docker $USER
        if [ $? -eq 0 ]; then
            echo "成功将用户 $USER 添加到docker组"
        else
            echo "将用户 $USER 添加到docker组时出错" >&2
        fi
    fi
done

echo "权限授予完成。用户需要重新登录才能使权限生效。"
echo "脚本执行结束，所有符合条件的用户已添加到docker组。"
# 重载systemd daemon配置
if command -v systemctl > /dev/null; then
    echo "正在重载systemd daemon..."
    systemctl daemon-reload
    echo "systemd daemon已重载"
fi

# 询问是否重启Docker服务
echo ""
read -p "是否重启Docker服务以确保权限更改生效？(y/n): " -n 1 -r
echo ""
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "正在重启Docker服务..."
    if command -v systemctl > /dev/null; then
        systemctl restart docker
        echo "Docker服务已重启"
    else
        service docker restart
        echo "Docker服务已重启"
    fi
    echo "完成！用户在下次登录后将能够使用Docker命令"
else
    echo "跳过Docker服务重启。建议稍后手动重启Docker服务或重启系统。"
    echo "完成！用户在下次登录后将能够使用Docker命令"
fi
