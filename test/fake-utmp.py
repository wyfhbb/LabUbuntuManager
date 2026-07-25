#!/usr/bin/env python3
"""往 /var/run/utmp 写两条登录会话，让容器里的 `who` 有真实输出。

容器里没有 login/sshd，不会有人写 utmp，`user who` 也就无从演示。
这里按 glibc 的 struct utmp（x86_64，384 字节）手工拼记录：

    偏移 0   short  ut_type      7 = USER_PROCESS
    偏移 4   int32  ut_pid
    偏移 8   char   ut_line[32]  终端名
    偏移 40  char   ut_id[4]
    偏移 44  char   ut_user[32]  用户名
    偏移 76  char   ut_host[256] 来源（SSH 客户端 IP）
    偏移 340 int32  ut_tv.tv_sec 登录时间
    偏移 348 int32  ut_addr_v6[0]

用法：fake-utmp.py <用户名> <终端> <来源IP或-> <几小时前登录>  [可重复多组]
"""
import os
import socket
import struct
import sys
import time

UTMP = "/var/run/utmp"
RECORD_SIZE = 384
USER_PROCESS = 7


def record(user, line, host, hours_ago, pid=1):
    # pid 必须是活着的进程：who 会逐条检查 ut_pid 是否还在，进程没了就不显示这条会话。
    # 容器里 PID 1 一定在，拿它当占位。
    buf = bytearray(RECORD_SIZE)
    struct.pack_into("<h", buf, 0, USER_PROCESS)
    struct.pack_into("<i", buf, 4, pid)
    struct.pack_into("<32s", buf, 8, line.encode())
    struct.pack_into("<4s", buf, 40, line[-4:].encode())
    struct.pack_into("<32s", buf, 44, user.encode())
    if host != "-":
        struct.pack_into("<256s", buf, 76, host.encode())
    struct.pack_into("<i", buf, 340, int(time.time() - hours_ago * 3600))
    if host != "-" and host.count(".") == 3:
        struct.pack_into("<4s", buf, 348, socket.inet_aton(host))
    return bytes(buf)


def main(argv):
    args = argv[1:]
    if not args or len(args) % 4 != 0:
        sys.exit("用法: fake-utmp.py <用户> <终端> <来源IP或-> <几小时前> ...")

    os.makedirs(os.path.dirname(UTMP), exist_ok=True)
    with open(UTMP, "wb") as f:
        for i in range(0, len(args), 4):
            user, line, host, hours = args[i:i + 4]
            f.write(record(user, line, host, float(hours)))


if __name__ == "__main__":
    main(sys.argv)
