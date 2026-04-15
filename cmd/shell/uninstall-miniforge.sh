#!/bin/bash
# Miniforge3 卸载脚本（由 server-mgr motd init 生成）
# 使用方法：bash /usr/local/lib/server-mgr/motd/uninstall-miniforge.sh

INSTALL_DIR=~/miniforge3

echo "======================================"
echo "       Miniforge 卸载工具             "
echo "======================================"
echo ""
echo "此脚本将从您的目录中卸载 Miniforge"
echo ""
echo "卸载路径: $INSTALL_DIR"
echo ""
echo "警告: 此操作将删除所有 conda 环境和安装的包!"
echo ""
read -p "确定要卸载 Miniforge 吗？请输入 'yes' 确认: " confirm

if [[ $confirm != "yes" ]]; then
    echo "卸载已取消"
    exit 0
fi

# 检查 Miniforge 是否安装
if [ ! -d "$INSTALL_DIR" ]; then
    echo "错误: 在 $INSTALL_DIR 未找到 Miniforge 安装"
    exit 1
fi

# 先运行 conda 的卸载命令以移除初始化脚本
if [ -f "$INSTALL_DIR/bin/conda" ]; then
    echo "正在移除 conda 初始化脚本..."
    $INSTALL_DIR/bin/conda init --reverse --all
fi

# 删除安装目录
echo "正在删除 Miniforge 安装目录..."
rm -rf $INSTALL_DIR

# 清理 shell 配置文件中可能残留的 conda 初始化代码
echo "正在清理 shell 配置文件..."
for rcfile in ~/.bashrc ~/.zshrc ~/.bash_profile ~/.profile; do
    if [ -f "$rcfile" ]; then
        cp "$rcfile" "${rcfile}.bak"
        sed -i '/# >>> conda initialize >>>/,/# <<< conda initialize <<</d' "$rcfile"
        echo "已清理 $rcfile"
    fi
done

echo "Miniforge 卸载完成"
