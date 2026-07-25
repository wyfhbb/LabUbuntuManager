package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "server-mgr",
	Short: "Ubuntu 服务器管理 CLI",
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		fmt.Print(`server-mgr — 实验室 Ubuntu 服务器管理工具

用法:
  server-mgr <命令> [子命令] [选项]

━━ 安装与版本 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  install                         安装到 /usr/local/bin 并写入默认配置（需要 root）
                                    同时安装 disk-usage 快捷命令
  version                         显示版本、编译时间与 git commit
  uninstall                       清理二进制、定时任务、MOTD 改动（需要 root）
    -y, --yes                       跳过确认
                                    保留 /var/log/disk-usage 与 /usr/local/lib/server-mgr

  配置文件: /usr/local/lib/server-mgr/config.conf（分区使用率警戒线、单用户占用
            告警线、日志保留天数、定时任务时间、不活跃天数阈值）

━━ 磁盘管理 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  disk                            列出磁盘挂载点及容量（过滤 snap/loop）
  disk usage                      各用户磁盘使用量，所有用户可用
    -m, --me                        只显示当前用户
    -s, --sort total|user           排序列（默认 total 降序）
    -r, --reverse                   反向排序
  disk warn                       占用超标的用户点名写入 MOTD 警告（需要 root）
    --gb 500                        用户总占用告警线（默认取自 config.conf）
                                    每日统计跑完后自动触发，通常无需手动执行
                                    分区使用率超警戒线是另一条链路：实时计算，
                                    直接在 MOTD 顶部提示，不受本命令影响
  disk monitor enable             启用每日统计定时任务（需要 root）
                                    同时安装 disk-usage 快捷命令供所有用户使用
  disk monitor disable            禁用定时任务（需要 root）
  disk monitor status             查看定时任务状态及最近统计时间
  disk monitor run                立即执行一次统计（需要 root）

━━ GPU 管理 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  gpu status                      每张卡的显存/利用率/温度/功耗、驱动与 CUDA 版本
  gpu top                         GPU 进程按用户聚合，看清谁占着哪张卡
                                    两条命令均无需 root，所有用户可用
                                    驱动异常（如升级后未重启导致的版本不一致）
                                    会给出结论、成因与处理步骤，MOTD 同步标红

━━ APT 源管理 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  source show                     查看当前 APT 镜像源
  source set <mirror>             切换镜像源
    可选: aliyun / tsinghua / ustc / bfsu / official
  source restore                  从备份还原 APT 源

━━ 用户管理 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  user list                       列出所有用户及数据目录映射
  user add <用户名>               创建新用户（在数据盘建立工作目录）
  user del <用户名>               删除用户
    --purge                         同时删除家目录及各数据盘目录
  user passwd <用户名>            修改用户密码
  user who                        当前登录会话（SSH 来源 IP、时长，含推断的 VSCode Remote）
  user top                        按用户聚合的 CPU/内存排行，标出长期占用大内存的进程
                                    两条命令均无需 root，所有用户可用
  user inactive list              列出所有用户的未登录天数
  user inactive warn              将不活跃用户警告写入 MOTD（需要 root）
    --days 180                      超过多少天未登录则警告（默认 180）
  user inactive purge             删除超过指定天数未登录的用户（需要 root）
    --days 180                      超过多少天未登录则删除（默认 180）
  user inactive monitor enable    启用每日自动检查（每天 02:00，需要 root）
    --days 180                      不活跃阈值天数（默认 180）
  user inactive monitor disable   禁用每日自动检查（需要 root）
  user inactive monitor status    查看定时任务状态

━━ MOTD 管理 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  motd set                          启用实验室自定义 MOTD（需要 root）
                                      禁用系统默认脚本，安装自定义欢迎信息
                                      并注入 VSCode Remote 终端的 MOTD 显示
  motd show                         预览当前 MOTD 输出
  motd status                       检查脚本、注入、警告是否都到位
  motd reset                        恢复系统默认 MOTD（需要 root）

━━ 主动告警 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  notify config                   配置推送渠道：企业微信 Webhook / SMTP 邮件（需要 root）
  notify test                     发一条测试消息验证配置（需要 root）
  notify check                    扫描分区使用率/GPU/需重启三源并去重推送（需要 root）
                                    每日磁盘统计后自动触发；同一告警默认 24h 内不重复推送
                                    配置落盘 /usr/local/lib/server-mgr/notify.conf（0600）

━━ 审计 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  audit                           查看写操作审计日志（需要 root，日志 0600）
    --user <名>                     只看涉及该用户（执行者或目标）的记录
    --since <时间>                  只看该时间之后（如 2026-07-01 或 "2026-07-01 12:00:00"）
                                    记录用户增删改、purge、换源、镜像、MOTD、安装卸载等
                                    成功完成的写操作，落盘 /var/log/server-mgr/audit.log

━━ Docker 管理 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  docker check                      检测 Docker 安装状态
  docker install                    安装 Docker（使用 BFSU 源，需要 root）
  docker perm                       查看各用户 Docker 权限
  docker perm add <用户名>          授予用户免 sudo 使用 docker（需要 root）
                                      注意：等价于授予 root 权限，需二次确认
  docker perm del <用户名>          收回用户的 docker 权限（需要 root）
  docker mirror set [镜像地址...]   配置镜像加速地址（需要 root）
                                      不带参数则使用内置默认地址

使用 "server-mgr <命令> --help" 查看具体命令的选项说明。
`)
	})
}
