#!/bin/bash
# Miniforge3 安装脚本（由 server-mgr motd init 生成）
# 使用方法：bash /usr/local/lib/server-mgr/motd/install-miniforge.sh

set -e

INSTALL_DIR=~/miniforge3

# 如果目录不存在则创建
mkdir -p $INSTALL_DIR

# 使用清华源镜像下载最新版 Miniforge 安装程序
echo "正在从清华源镜像下载 Miniforge3 安装程序..."
wget https://mirrors.tuna.tsinghua.edu.cn/github-release/conda-forge/miniforge/LatestRelease/Miniforge3-Linux-x86_64.sh -O $INSTALL_DIR/miniforge.sh

# 使安装程序可执行
chmod +x $INSTALL_DIR/miniforge.sh

# 运行安装程序：批处理模式(-b)，更新已有安装(-u)，指定安装路径(-p)
echo "正在将 Miniforge3 安装到 $INSTALL_DIR..."
bash $INSTALL_DIR/miniforge.sh -b -u -p $INSTALL_DIR

# 安装完成后删除安装程序
echo "正在清理安装文件..."
rm -f $INSTALL_DIR/miniforge.sh

# 激活基础环境
echo "正在激活 Miniforge 环境..."
source $INSTALL_DIR/bin/activate

# 为所有可用的 shell 初始化 conda
echo "正在为所有 shell 初始化 conda..."
$INSTALL_DIR/bin/conda init --all

echo ""
echo "Miniforge3 安装完成！"
echo "要开始使用 conda，请重启终端或运行: source $INSTALL_DIR/bin/activate"
