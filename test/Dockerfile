# server-mgr 端到端测试环境
#
# 一台"干净的实验室 Ubuntu 服务器"，用来跑 server-mgr 的全部命令而不碰开发机：
#   - Ubuntu 24.04（与目标机型一致，APT 源为 DEB822 格式的 ubuntu.sources）
#   - cron / sudo / procps / iproute2 / zsh / docker CLI 等被命令调用的外部工具
#   - 多块"物理硬盘"与挂载点由 setup-env.sh 在容器启动后模拟（见该脚本）
#
# APT 源保持镜像自带的官方源不动：source show 的初始状态就是"Ubuntu 官方"，
# 换源效果一眼可见。
#
# 构建与运行统一走 run-e2e.sh，不要直接 docker run（需要 SYS_ADMIN 才能挂载模拟盘）。
FROM ubuntu:24.04

# policy-rc.d 让 docker.io 的 postinst 不去启动服务（容器里没有 systemd）
RUN printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d && chmod +x /usr/sbin/policy-rc.d \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        ca-certificates \
        cron \
        docker.io \
        iproute2 \
        procps \
        python3 \
        sudo \
        tzdata \
        util-linux \
        zsh \
    && rm -rf /var/lib/apt/lists/*

# 与 Ubuntu 云镜像/服务器安装的默认值一致。
# 注意 who 的日期格式随 locale 变：C/POSIX 下是 "Jul 25 18:45"，
# 其余 locale 下是 "2026-07-25 18:45"，user who 的解析依赖后者。
ENV LANG=C.UTF-8
ENV TZ=Asia/Shanghai
RUN ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && echo "Asia/Shanghai" > /etc/timezone

COPY setup-env.sh e2e.sh fake-wechat.py fake-utmp.py fake-nvidia-smi /opt/test/
RUN chmod +x /opt/test/setup-env.sh /opt/test/e2e.sh /opt/test/fake-wechat.py \
        /opt/test/fake-utmp.py /opt/test/fake-nvidia-smi

WORKDIR /root
CMD ["/bin/bash"]
