#!/bin/bash
# 在容器内把一台"空壳 Ubuntu"布置成实验室服务器的样子，供 e2e.sh 跑全量命令。
#
# 这里模拟的东西只有一类：**server-mgr 读取的系统状态**（挂载表、/sys/block、
# lastlog、MOTD 脚本、reboot-required）。server-mgr 自己的逻辑一律走真实代码路径。
#
# 需要容器带 --cap-add SYS_ADMIN（挂载）。
set -euo pipefail

log() { printf '\033[36m[setup]\033[0m %s\n' "$*"; }

# ── 1. 摘掉 Docker 注入的 bind mount ───────────────────────────────────────────
# docker 会把宿主机的 /etc/hosts、/etc/hostname、/etc/resolv.conf 以及被测二进制
# bind 进来，它们在 /proc/mounts 里带着宿主机的真实设备名（如 /dev/nvme0n1p2），
# 会被 server-mgr 当成本机的一块盘。内容留下，挂载摘掉。
for f in /etc/resolv.conf /etc/hostname /etc/hosts /opt/server-mgr; do
    if mountpoint -q "$f"; then
        cp "$f" "$f.keep"
        umount "$f"
        cat "$f.keep" >"$f"
        chmod --reference="$f.keep" "$f"
        rm -f "$f.keep"
    fi
done
log "已摘除 docker 注入的 bind mount（/etc/{hosts,hostname,resolv.conf}、/opt/server-mgr）"

# ── 2. 模拟三块物理硬盘及其分区 ────────────────────────────────────────────────
# 手法：mount 的 source 字段会原样落进 /proc/mounts，tmpfs 也不例外。
# 于是 `mount -t tmpfs /dev/sdb1 /data` 就得到一条与真实分区无异的挂载记录，
# server-mgr 的 /proc/mounts 解析、按物理盘分组、statfs 容量统计全部走真实代码。
#
#   /dev/sda1 → /boot          （系统盘，240 GB 的 sda）
#   /dev/sda2 → /home
#   /dev/sdb1 → /data          （数据盘 1，2 TB 的 sdb）
#   /dev/sdb2 → /workspace     （故意做小，方便撑到警戒线以上）
#   /dev/sdc1 → /mnt/storage   （数据盘 2，1 TB 的 sdc）
mkdir -p /boot /home /data /workspace /mnt/storage
mount -t tmpfs -o size=2G    /dev/sda1 /boot
mount -t tmpfs -o size=236G  /dev/sda2 /home
mount -t tmpfs -o size=2046G /dev/sdb1 /data
mount -t tmpfs -o size=2G    /dev/sdb2 /workspace
mount -t tmpfs -o size=1024G /dev/sdc1 /mnt/storage
chmod 755 /boot /home /data /workspace /mnt/storage
log "已挂载 5 个模拟分区（sda1/sda2/sdb1/sdb2/sdc1）"

# ── 3. 模拟 /sys/block ────────────────────────────────────────────────────────
# server-mgr 从 /sys/block/<盘>/size 读整盘容量（扇区数 × 512B）。
# 容器里没有 sda/sdb/sdc，用 bind mount 顶上去。
mkdir -p /run/fake-sys/sda /run/fake-sys/sdb /run/fake-sys/sdc
echo 503316480 >/run/fake-sys/sda/size  # 240 GB
echo 4294967296 >/run/fake-sys/sdb/size # 2 TB
echo 2147483648 >/run/fake-sys/sdc/size # 1 TB
mount --bind /run/fake-sys /sys/block
log "已伪造 /sys/block（sda 240GB / sdb 2TB / sdc 1TB）"

# ── 4. 系统默认 MOTD 脚本 ──────────────────────────────────────────────────────
# 官方 ubuntu 镜像把 /etc/update-motd.d 清空了，这里补两个仿真的默认脚本，
# 用来验证 motd set 的"禁用默认脚本"与 motd reset 的"恢复默认脚本"。
mkdir -p /etc/update-motd.d
cat >/etc/update-motd.d/00-header <<'EOF'
#!/bin/sh
echo "Welcome to Ubuntu 24.04.3 LTS (GNU/Linux 6.8.0-generic x86_64)"
EOF
cat >/etc/update-motd.d/10-help-text <<'EOF'
#!/bin/sh
echo " * Documentation:  https://help.ubuntu.com"
EOF
chmod +x /etc/update-motd.d/00-header /etc/update-motd.d/10-help-text
log "已放置 2 个系统默认 MOTD 脚本"

# ── 5. 系统提醒素材 ────────────────────────────────────────────────────────────
# MOTD 的"系统提醒"段落与 notify 的"需要重启"告警源读这两处。
mkdir -p /var/lib/update-notifier
cat >/var/lib/update-notifier/updates-available <<'EOF'
12 updates can be applied immediately.
3 of these updates are standard security updates.
EOF
log "已写入 updates-available（reboot-required 留给 e2e.sh 按需制造）"

# ── 6. 一个"很久没登录"的老用户 ────────────────────────────────────────────────
# user inactive 依赖 lastlog。lastlog 是按 UID 索引的定长二进制文件
# （struct lastlog：int32 时间 + char[32] 终端 + char[256] 来源 = 292 字节），
# 这里直接写一条 400 天前的登录记录，模拟长期不登录的账号。
useradd -m -s /bin/bash -c "Wang Wu" olduser
python3 - <<'PY'
import os, struct, time

RECORD = struct.Struct('<i32s256s')          # ll_time / ll_line / ll_host
path = '/var/log/lastlog'
os.makedirs(os.path.dirname(path), exist_ok=True)
if not os.path.exists(path):
    open(path, 'wb').close()

uid = int(os.popen('id -u olduser').read().strip())
ts = int(time.time()) - 400 * 86400
with open(path, 'r+b') as f:
    f.seek(uid * RECORD.size)
    f.write(RECORD.pack(ts, b'pts/0', b'192.168.10.66'))
PY
log "已创建 olduser（lastlog 记录：400 天前）"

log "环境就绪"
