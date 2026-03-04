# AGENTS.md

## Project Overview
A CLI-based Ubuntu server management tool written in Go, using the `cobra` framework.
Binary name: `server-mgr`

## Project Structure
```
.
├── cmd/
│   ├── root.go      # rootCmd definition
│   ├── user.go      # `server-mgr user` subcommand group
│   └── source.go    # `server-mgr source` subcommand group
├── main.go
└── go.mod
```

## Tech Constraints
- Go standard library only; the sole third-party dependency is `github.com/spf13/cobra`
- Target OS: Ubuntu (Linux); no cross-platform compatibility required
- All subcommands must be registered to `rootCmd` inside their own `init()`

## Code Conventions
- Error messages to `os.Stderr`; exit with `os.Exit(1)` on fatal errors
- All commands requiring root: check `os.Getuid() == 0` before any privileged operation, print actionable hint and exit non-zero if not root
- System commands: use `os/exec` with `cmd.Stdout = os.Stdout` and `cmd.Stderr = os.Stderr` for real-time streaming output
- No `log` package; use `fmt.Fprintf(os.Stderr, ...)` for errors

## Key Implementation Details

### user.go
- `user list`: parse `/etc/passwd`, filter UID >= 1000 and shell != `/usr/sbin/nologin`, format with `text/tabwriter` (columns: username, UID, home)
- `user add <username>`: parse `/proc/mounts` for `/dev/`-prefixed mountpoints → `syscall.Statfs` for free space → numbered interactive selection → `useradd -m -d <mount>/<username> -s /bin/bash <username>` → `chage -d 0 <username>`
- `user del <username>`: `userdel <username>`; with `--purge` flag: `userdel -r <username>`
- `user passwd <username>`: `passwd <username>`

### source.go
- `source set <mirror>`: accepts `aliyun` / `tsinghua` / `ustc` / `official`
- Parse `VERSION_CODENAME` from `/etc/os-release`
- Backup `/etc/apt/sources.list` → `/etc/apt/sources.list.bak` before any write
- Write mirror content then run `apt-get update`

## Testing
- Manual testing only (no unit test files needed unless explicitly requested)
- Test as root or with `sudo`