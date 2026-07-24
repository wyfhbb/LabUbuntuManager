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

  配置文件: /usr/local/lib/server-mgr/config.conf（磁盘警戒线、日志保留天数、
            定时任务时间、不活跃天数阈值）

━━ 磁盘管理 ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  disk                            列出磁盘挂载点及容量（过滤 snap/loop）
  disk usage                      各用户磁盘使用量，所有用户可用
    -m, --me                        只显示当前用户
    -s, --sort total|user           排序列（默认 total 降序）
    -r, --reverse                   反向排序
  disk monitor enable             启用每日统计定时任务（需要 root）
                                    同时安装 disk-usage 快捷命令供所有用户使用
  disk monitor disable            禁用定时任务（需要 root）
  disk monitor status             查看定时任务状态及最近统计时间
  disk monitor run                立即执行一次统计（需要 root）

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
