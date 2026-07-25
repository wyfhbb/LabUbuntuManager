#!/bin/bash
# server-mgr 全命令端到端演练：逐条执行、记录输入与输出。
#
# 由 run-e2e.sh 在容器内调用（setup-env.sh 已先跑过）。
# 刻意不加 set -e：错误路径本身也是要记录的输出。
set -u

BIN=/opt/server-mgr           # 未安装状态下使用的二进制
MGR=/usr/local/bin/server-mgr # install 之后的系统路径

section() { printf '\n\n========== %s ==========\n' "$*"; }
note() { printf '\n--- %s\n' "$*"; }

run() { # run <命令...>
    printf '\n$ %s\n' "$*"
    "$@"
    local rc=$?
    [ "$rc" -ne 0 ] && printf '[退出码 %d]\n' "$rc"
    return 0
}

run_in() { # run_in <喂给 stdin 的内容> <命令...>
    local input=$1
    shift
    printf '\n$ %s\n' "$*"
    printf '%s' "$input" | "$@"
    local rc=$?
    [ "$rc" -ne 0 ] && printf '[退出码 %d]\n' "$rc"
    return 0
}

sh_run() { # sh_run <shell 片段>：需要管道/重定向/环境变量时使用
    printf '\n$ %s\n' "$1"
    bash -c "$1"
    local rc=$?
    [ "$rc" -ne 0 ] && printf '[退出码 %d]\n' "$rc"
    return 0
}

# ══════════════════════════════════════════════════════════════════════════════
section "0. 测试环境概览"
run uname -srm
sh_run 'grep PRETTY_NAME /etc/os-release'
run hostname
note "模拟出来的挂载表（server-mgr 看到的就是这些）"
sh_run 'grep ^/dev/ /proc/mounts'
note "模拟出来的整盘容量"
sh_run 'for d in /sys/block/*; do echo "$d/size = $(cat "$d"/size) 扇区"; done'
note "确认本机没有 NVIDIA 显卡与驱动"
sh_run 'command -v nvidia-smi || echo "nvidia-smi: 不存在"'
sh_run 'grep -l 0x10de /sys/bus/pci/devices/*/vendor 2>/dev/null || echo "PCI 总线上没有 NVIDIA 设备"'

# ══════════════════════════════════════════════════════════════════════════════
section "1. 帮助与版本"
run $BIN --help
run $BIN version

# ══════════════════════════════════════════════════════════════════════════════
section "2. install（统一安装入口）"
note "未装时先看一眼相关路径"
sh_run 'ls -l /usr/local/bin/ /usr/local/lib/ 2>&1'
run $BIN install
note "安装后生成的配置文件"
sh_run 'cat /usr/local/lib/server-mgr/config.conf'
sh_run 'ls -l /usr/local/bin/server-mgr /usr/local/bin/disk-usage'
run $MGR version
note "重复执行 install（幂等，不覆盖已有配置）"
run $MGR install

# ══════════════════════════════════════════════════════════════════════════════
section "3. disk：多硬盘识别"
run $MGR disk

# ══════════════════════════════════════════════════════════════════════════════
section "4. disk monitor：每日用量统计"
note "统计还没启用时查用量"
run $MGR disk usage
run $MGR disk monitor status
run $MGR disk monitor enable
run $MGR disk monitor status
note "cron 与统计脚本落盘内容"
sh_run 'cat /etc/cron.d/server-mgr-disk'
sh_run 'ls -l /var/log/disk-usage/'
sh_run 'cat /var/log/disk-usage/current-usage.txt'
run $MGR disk usage

# ══════════════════════════════════════════════════════════════════════════════
section "5. user：创建 / 列表 / 多盘工作目录"
run $MGR user list
note "交互式创建用户（依次输入：全名 / 密码 / 确认密码 / y）"
run_in 'Zhang San
LabPass123
LabPass123
y
' $MGR user add zhangsan
note "创建第二个用户"
run_in 'Li Si
LabPass456
LabPass456
y
' $MGR user add lisi
run $MGR user list
note "家目录里的符号链接指向各数据盘"
sh_run 'ls -l /home/zhangsan/'
sh_run 'ls -ld /data/zhangsan /workspace/zhangsan /mnt/storage/zhangsan'
sh_run 'grep ^zhangsan: /etc/passwd'
note "取消创建（确认处输入 n）"
run_in 'Zhao Liu
pw
pw
n
' $MGR user add zhaoliu
note "修改密码（passwd 交互，输入两次新密码）"
run_in 'NewLabPass789
NewLabPass789
' $MGR user passwd lisi

# ══════════════════════════════════════════════════════════════════════════════
section "6. 磁盘告警链路"
note "制造占用：zhangsan 在 /workspace 放 1.74G、/data 放 220M"
sh_run 'dd if=/dev/zero of=/workspace/zhangsan/dataset.bin bs=1M count=1740 status=none && \
        dd if=/dev/zero of=/data/zhangsan/checkpoints.bin bs=1M count=220 status=none && \
        chown zhangsan:zhangsan /workspace/zhangsan/dataset.bin /data/zhangsan/checkpoints.bin && \
        df -h /workspace /data | cat'
note "分区使用率超 80% 警戒线的分区带 [!]"
run $MGR disk
run $MGR disk monitor run
sh_run 'cat /var/log/disk-usage/current-usage.txt'
run $MGR disk usage
run $MGR disk usage --sort user
run $MGR disk usage --sort user --reverse
note "默认阈值 500GB：没人超标（并清除旧的磁盘告警文件）"
run $MGR disk warn
note "顺带验证：老版本的单文件告警会被迁移进 warnings.d/"
sh_run 'mkdir -p /usr/local/lib/server-mgr/motd && \
        echo "⚠ 这是老版本 warnings.txt 里的内容" > /usr/local/lib/server-mgr/motd/warnings.txt && \
        ls /usr/local/lib/server-mgr/motd/'
note "把阈值压到 1GB：zhangsan 超标"
run $MGR disk warn --gb 1
sh_run 'ls -l /usr/local/lib/server-mgr/motd/ /usr/local/lib/server-mgr/motd/warnings.d/'
sh_run 'cat /usr/local/lib/server-mgr/motd/warnings.d/10-disk.txt'

# ══════════════════════════════════════════════════════════════════════════════
section "7. MOTD"
run $MGR motd status
run $MGR motd set
run $MGR motd status
note "预览 MOTD（分区告警在顶部，各来源告警在底部）"
run $MGR motd show
note "注入 /etc/bash.bashrc 与 /etc/zsh/zshrc 的片段"
sh_run 'tail -6 /etc/bash.bashrc'
sh_run 'tail -6 /etc/zsh/zshrc'
sh_run 'bash -n /etc/bash.bashrc && echo "bash -n 语法检查通过"'
note "紧邻片段再放一个 if 块，重复 motd set 不应破坏它（历史 bug 回归）"
sh_run 'printf "if [ -n \"\$PS1\" ]; then\n  : lab-extra\nfi\n" >> /etc/bash.bashrc && tail -4 /etc/bash.bashrc'
run $MGR motd set
sh_run 'bash -n /etc/bash.bashrc && echo "bash -n 语法检查通过"; \
        grep -c "server-mgr vscode-motd begin" /etc/bash.bashrc | xargs echo "注入片段数量:"; \
        grep -c "lab-extra" /etc/bash.bashrc | xargs echo "相邻 if 块仍在:"'
note "系统默认 MOTD 脚本已被禁用（去掉可执行位）"
sh_run 'ls -l /etc/update-motd.d/ && cat /usr/local/lib/server-mgr/motd/disabled-scripts.txt'
note "生成的环境初始化脚本"
sh_run 'ls -l /usr/local/lib/server-mgr/motd/init/'
note "自定义欢迎语可直接编辑"
sh_run 'echo "欢迎使用 AI 实验室 GPU 服务器 lab-server-01，问题请联系管理员 wyf。" > /usr/local/lib/server-mgr/motd/header.txt'
run $MGR motd show

# ══════════════════════════════════════════════════════════════════════════════
section "8. GPU：本机真实状态（无 NVIDIA 显卡）"
run $MGR gpu status
run $MGR gpu top
note "MOTD 里 GPU 段落整段跳过（见上面 motd show 的输出）"

section "8b. GPU：用 fake-nvidia-smi 桩验证各分支（数值非真实硬件）"
note "准备三个 PID 供进程归属：zhangsan 的、root 的、以及一个已退出的"
sh_run 'su zhangsan -c "nohup sleep 900 >/dev/null 2>&1 &" ; nohup sleep 900 >/dev/null 2>&1 & \
        sleep 0.5; pgrep -u zhangsan -n sleep >/tmp/fake-gpu-pids; \
        pgrep -u root -n sleep >>/tmp/fake-gpu-pids; echo 999999 >>/tmp/fake-gpu-pids; \
        cat /tmp/fake-gpu-pids'
sh_run 'install -m 755 /opt/test/fake-nvidia-smi /usr/local/bin/nvidia-smi && command -v nvidia-smi'
run $MGR gpu status
run $MGR gpu top
note "两张卡都空闲、无计算进程"
sh_run "SMI_SCENARIO=idle $MGR gpu top"
note "MOTD 的 GPU 概览段落"
sh_run "$MGR motd show | tail -20"
note "故障分类：驱动与内核模块版本不一致（apt 升级驱动后没重启）"
sh_run "SMI_SCENARIO=mismatch $MGR gpu status"
note "故障分类：内核模块未加载"
sh_run "SMI_SCENARIO=nodriver $MGR gpu status"
note "故障分类：认不到卡"
sh_run "SMI_SCENARIO=nodevice $MGR gpu status"
note "故障分类：权限不足"
sh_run "SMI_SCENARIO=noperm $MGR gpu status"
note "故障分类：NVML 未知错误"
sh_run "SMI_SCENARIO=nvmlunknown $MGR gpu status"
note "故障分类：查询超时（2 秒超时生效，整条命令约 2 秒返回）"
sh_run "time SMI_SCENARIO=hang $MGR gpu status"
note "MOTD 在 GPU 异常时只标红一行，不把整屏排障说明塞进登录信息"
sh_run "SMI_SCENARIO=mismatch $MGR motd show | grep -B2 -A1 GPU"
note "有卡但没装驱动：删掉 nvidia-smi，伪造一个 NVIDIA PCI 设备"
sh_run 'rm -f /usr/local/bin/nvidia-smi && mkdir -p /run/fake-pci/0000:01:00.0 && \
        echo 0x10de > /run/fake-pci/0000:01:00.0/vendor && \
        echo 0x030000 > /run/fake-pci/0000:01:00.0/class && \
        mount --bind /run/fake-pci /sys/bus/pci/devices'
run $MGR gpu status
sh_run 'umount /sys/bus/pci/devices'
note "恢复成本机真实状态（无卡）"
run $MGR gpu status

# ══════════════════════════════════════════════════════════════════════════════
section "9. 运行时可见性：user who / user top"
note "写两条 utmp 登录记录（容器里没有 sshd，who 否则没有输出）"
sh_run 'python3 /opt/test/fake-utmp.py zhangsan pts/0 192.168.10.42 3.5 root pts/1 - 0.2 && who'
note "再制造一个 VSCode Remote 进程"
sh_run 'su - zhangsan -c "mkdir -p ~/.vscode-server/bin/abc123 && cp /usr/bin/sleep ~/.vscode-server/bin/abc123/node"; \
        su - zhangsan -c "nohup ~/.vscode-server/bin/abc123/node 900 >/dev/null 2>&1 &"; sleep 1; true'
run $MGR user who
note "C/POSIX locale 下 who 换成 \"Jul 25 18:45\"（三段、无年份）格式，同样要解析正确"
sh_run "LC_ALL=C who; echo '--- user who ---'; LC_ALL=C $MGR user who"
run $MGR user top

# ══════════════════════════════════════════════════════════════════════════════
section "10. 不活跃用户"
run $MGR user inactive list
run $MGR user inactive warn
run $MGR user inactive warn --days 365
sh_run 'cat /usr/local/lib/server-mgr/motd/warnings.d/20-inactive.txt'
note "两条告警（磁盘 + 不活跃）互不覆盖"
sh_run 'ls -l /usr/local/lib/server-mgr/motd/warnings.d/'
run $MGR motd status
run $MGR user inactive monitor enable --days 180
sh_run 'cat /etc/cron.d/server-mgr-inactive'
sh_run 'grep INACTIVE_DAYS /usr/local/lib/server-mgr/config.conf'
run $MGR user inactive monitor status
note "删除不活跃用户（确认处输入 n：不删）"
run_in 'n
' $MGR user inactive purge --days 180
note "确认处输入 y：删除"
run_in 'y
' $MGR user inactive purge --days 180
run $MGR user inactive list
run $MGR user inactive monitor status

# ══════════════════════════════════════════════════════════════════════════════
section "11. Docker"
run $MGR docker check
run $MGR docker install
run $MGR docker perm
note "授权（确认处输入 n：不授权）"
run_in 'n
' $MGR docker perm add lisi
note "授权（确认处输入 y）"
run_in 'y
' $MGR docker perm add lisi
run $MGR docker perm
sh_run 'grep ^docker: /etc/group'
note "重复授权"
run_in 'y
' $MGR docker perm add lisi
run $MGR docker perm del lisi
run $MGR docker perm del lisi
note "镜像加速：先放一份已有的 daemon.json，验证其他配置项不被破坏"
sh_run 'mkdir -p /etc/docker && printf "{\n  \"log-driver\": \"json-file\",\n  \"data-root\": \"/data/docker\"\n}\n" > /etc/docker/daemon.json && cat /etc/docker/daemon.json'
note "不带参数 = 用内置默认地址（重启提示处输入 n）"
run_in 'n
' $MGR docker mirror set
sh_run 'cat /etc/docker/daemon.json; echo "--- 备份 ---"; cat /etc/docker/daemon.json.bak'
note "显式指定多个地址"
run_in 'n
' $MGR docker mirror set https://docker.1ms.run https://hub.example.com
sh_run 'cat /etc/docker/daemon.json'
note "地址格式非法"
run $MGR docker mirror set docker.1ms.run

# ══════════════════════════════════════════════════════════════════════════════
section "12. APT 源"
run $MGR source show
run $MGR source set tsinghua
sh_run 'cat /etc/apt/sources.list.d/ubuntu.sources'
run $MGR source show
run $MGR source restore
run $MGR source set nosuchmirror

# ══════════════════════════════════════════════════════════════════════════════
section "13. notify：主动告警推送"
note "起一个假的企业微信机器人（本地 HTTP，收到就回 errcode:0）"
sh_run 'nohup python3 /opt/test/fake-wechat.py >/tmp/fake-wechat.log 2>&1 & sleep 1; echo started'
note "还没配置任何渠道时"
run $MGR notify test
run $MGR notify check
note "交互式配置（webhook / SMTP 留空回车 / 静默窗口回车保留 24h）"
run_in 'http://127.0.0.1:18080/webhook


' $MGR notify config
sh_run 'ls -l /usr/local/lib/server-mgr/notify.conf && cat /usr/local/lib/server-mgr/notify.conf'
run $MGR notify test
sh_run 'cat /tmp/webhook-received.log'
note '制造"需要重启"，再跑一次 check（应推送 分区超线 + 需重启）'
sh_run 'touch /var/run/reboot-required'
run $MGR notify check
sh_run 'cat /tmp/webhook-received.log'
note "静默窗口内重复执行不会再推"
run $MGR notify check
sh_run 'cat /usr/local/lib/server-mgr/notify/state'
note "把去重状态的时间戳改成 25 小时前，超出静默窗口后会再推"
sh_run 'awk -v t=$(( $(date +%s) - 25*3600 )) -F"\t" "{print \$1 \"\t\" t}" /usr/local/lib/server-mgr/notify/state > /tmp/s && cat /tmp/s > /usr/local/lib/server-mgr/notify/state'
run $MGR notify check
note "每日统计脚本跑完会自动触发 disk warn + notify check"
sh_run 'tail -12 /usr/local/lib/server-mgr/daily-disk-monitor.sh'
note "MOTD 也会显示需要重启与 apt 更新"
run $MGR motd show

# ══════════════════════════════════════════════════════════════════════════════
section "14. audit：写操作审计"
note '换个管理员账号用 sudo 执行一次写操作，验证审计记的"执行者"是谁'
sh_run 'useradd -m -s /bin/bash -c "Lab Admin" wyf && echo "wyf ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/wyf && chmod 440 /etc/sudoers.d/wyf'
sh_run 'su - wyf -c "echo y | sudo /usr/local/bin/server-mgr docker perm add lisi"'
run $MGR audit --user wyf
run $MGR audit
run $MGR audit --user zhangsan
sh_run "$MGR audit --since \$(date +%Y-%m-%d)"
run $MGR audit --since 2030-01-01
run $MGR audit --since 昨天
sh_run 'ls -ld /var/log/server-mgr && ls -l /var/log/server-mgr/audit.log'

# ══════════════════════════════════════════════════════════════════════════════
section "15. 权限与错误边界"
note "普通用户可用的只读命令"
sh_run 'su - zhangsan -c "disk-usage"'
sh_run 'su - zhangsan -c "disk-usage --me"'
sh_run 'su - zhangsan -c "/usr/local/bin/server-mgr disk"'
sh_run 'su - zhangsan -c "/usr/local/bin/server-mgr user who"'
sh_run 'su - zhangsan -c "/usr/local/bin/server-mgr gpu status"'
note "普通用户渲染 MOTD（读得到告警，不因无写权限报错）"
sh_run "su - zhangsan -c '/usr/local/bin/server-mgr motd render' | sed -n '1,16p'"
note "普通用户执行写操作"
sh_run 'su - zhangsan -c "/usr/local/bin/server-mgr user add someone"'
sh_run 'su - zhangsan -c "/usr/local/bin/server-mgr disk monitor enable"'
sh_run 'su - zhangsan -c "/usr/local/bin/server-mgr disk warn"'
sh_run 'su - zhangsan -c "/usr/local/bin/server-mgr audit"'
sh_run 'su - zhangsan -c "/usr/local/bin/server-mgr install"'
sh_run 'su - zhangsan -c "/usr/local/bin/server-mgr uninstall -y"'
note "两次密码不一致：只是重新提示，此时还没碰系统"
run_in 'Sun Qi
pw-a
pw-b
pw-c
pw-c
n
' $MGR user add sunqi
note "用户已存在"
run_in 'X
p
p
y
' $MGR user add zhangsan
note "家目录已存在：中止，不改动已存在的目录"
sh_run 'mkdir -p /home/ghost && touch /home/ghost/sentinel'
run_in 'X
p
p
y
' $MGR user add ghost
sh_run 'ls -l /home/ghost/ && id ghost 2>&1'
note "某块数据盘上已存在同名目录：同样中止"
sh_run 'mkdir -p /data/ghost2 && touch /data/ghost2/sentinel'
run_in 'X
p
p
y
' $MGR user add ghost2
sh_run 'ls -l /data/ghost2/ && id ghost2 2>&1'
note "用户名非法"
run $MGR user add ZhangSan
note "事务性：把 /workspace 挂成只读，user add 中途失败必须回滚干净"
sh_run 'mount -o remount,ro /workspace && grep " /workspace " /proc/mounts'
run_in 'Test Rollback
pw123
pw123
y
' $MGR user add rollbackuser
sh_run 'echo "--- 回滚后应当什么都不剩 ---"; id rollbackuser 2>&1; \
        ls -ld /home/rollbackuser /data/rollbackuser /mnt/storage/rollbackuser 2>&1'
note "被回滚的 user add 不写审计日志"
run $MGR audit --user rollbackuser
sh_run 'mount -o remount,rw /workspace'
note "不存在的用户 / 子命令拼错 / 极大阈值"
run_in 'y
' $MGR docker perm add nosuchuser
run $MGR docker perm ad lisi
run $MGR user inactive purge --days 9999
run $MGR source restore

# ══════════════════════════════════════════════════════════════════════════════
section "16. 清理：删用户、关监控、卸载"
note "删除用户但保留数据"
run $MGR user del lisi
sh_run 'ls -ld /home/lisi /data/lisi /workspace/lisi /mnt/storage/lisi 2>&1; id lisi 2>&1'
note "连数据一起删（先结束该用户名下的进程，否则 userdel 可能拒绝）"
sh_run 'pkill -u zhangsan; sleep 1; true'
run $MGR user del --purge zhangsan
sh_run 'ls -ld /home/zhangsan /data/zhangsan /workspace/zhangsan /mnt/storage/zhangsan 2>&1; id zhangsan 2>&1'
run $MGR audit --user zhangsan

note "单项关闭：恢复默认 MOTD、停掉两个定时任务"
run $MGR motd reset
run $MGR motd status
sh_run 'ls -l /etc/update-motd.d/; echo "--- bashrc 末尾 ---"; tail -4 /etc/bash.bashrc; \
        bash -n /etc/bash.bashrc && echo "bash -n 语法检查通过"'
run $MGR disk monitor disable
run $MGR user inactive monitor disable
sh_run 'ls -l /etc/cron.d/'

note "重新全部启用，好让 uninstall 演示完整清理"
sh_run "$MGR motd set >/dev/null && $MGR disk monitor enable >/dev/null && \
        $MGR user inactive monitor enable >/dev/null && ls /etc/cron.d/ /etc/update-motd.d/"
note "卸载：不带 -y 时会等确认（这里输入 n）"
run_in 'n
' $BIN uninstall
note "卸载（-y 跳过确认）"
run $BIN uninstall -y
sh_run 'ls -l /usr/local/bin/ /etc/cron.d/ /etc/update-motd.d/'
sh_run 'echo "--- bashrc 末尾 ---"; tail -4 /etc/bash.bashrc; bash -n /etc/bash.bashrc && echo "bash -n 语法检查通过"'
note "按设计保留的数据"
sh_run 'ls -l /usr/local/lib/server-mgr/ /var/log/disk-usage/ /var/log/server-mgr/'
run $BIN audit

printf '\n\n========== 演练结束 ==========\n'
