#!/usr/bin/env bash
# 在一次性 Docker 容器里跑 server-mgr 的全量端到端演练。
#
#   ./test/run-e2e.sh                 # 跑完输出到终端，同时存一份日志
#   ./test/run-e2e.sh -o out.log      # 指定日志路径
#   ./test/run-e2e.sh --shell         # 只把环境布置好，进容器手动折腾
#
# 为什么必须在容器里跑：这些命令会建用户、改 /etc/cron.d、动 MOTD、写
# /usr/local/lib —— 全是不该落在开发机上的系统级改动。容器 --rm 退出即净。
#
# 需要 --cap-add SYS_ADMIN：容器内要挂载模拟出来的多块硬盘（见 setup-env.sh）。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE=server-mgr-e2e
LOG="${TMPDIR:-/tmp}/server-mgr-e2e-$(date +%Y%m%d-%H%M%S).log"
MODE=run

while [ $# -gt 0 ]; do
    case "$1" in
    -o | --output)
        LOG="$2"
        shift 2
        ;;
    --shell)
        MODE=shell
        shift
        ;;
    -h | --help)
        sed -n '2,12p' "${BASH_SOURCE[0]}"
        exit 0
        ;;
    *)
        echo "未知参数: $1" >&2
        exit 2
        ;;
    esac
done

echo "==> 编译二进制（make build，注入版本号）"
make -C "$REPO_ROOT" build

echo "==> 构建测试镜像 $IMAGE"
docker build -t "$IMAGE" "$REPO_ROOT/test"

DOCKER_ARGS=(
    --rm
    --cap-add SYS_ADMIN
    --hostname lab-server-01
    -v "$REPO_ROOT/server-mgr:/opt/server-mgr:ro"
)

if [ "$MODE" = shell ]; then
    echo "==> 布置环境并进入交互 shell（二进制在 /opt/server-mgr）"
    exec docker run "${DOCKER_ARGS[@]}" -it "$IMAGE" \
        bash -c '/opt/test/setup-env.sh && exec bash'
fi

# 容器内先把 stderr 并进 stdout：docker 在非 TTY 下分两路传输，
# 分开传会让命令的正常输出与报错在日志里错位。
echo "==> 开跑，日志同时写入 $LOG"
docker run "${DOCKER_ARGS[@]}" "$IMAGE" \
    bash -c 'exec 2>&1; /opt/test/setup-env.sh && /opt/test/e2e.sh' | tee "$LOG"

echo
echo "==> 完成，日志: $LOG"
