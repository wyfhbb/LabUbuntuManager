.PHONY: build deploy test clean

-include .env

BINARY=server-mgr
SERVER?=$(DEPLOY_SERVER)
PORT?=$(DEPLOY_PORT)
REMOTE_PATH=~/lab_manager

# 版本信息：优先取 git 标签/commit，取不到时回落到占位值
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME?=$(shell date '+%Y-%m-%d %H:%M:%S')

# Docker 镜像加速地址，见 .env.example；多个用逗号分隔
DOCKER_MIRRORS?=https://docker.1ms.run

LDFLAGS=-X 'server-mgr/cmd.buildVersion=$(VERSION)' \
	-X 'server-mgr/cmd.buildCommit=$(GIT_COMMIT)' \
	-X 'server-mgr/cmd.buildTime=$(BUILD_TIME)' \
	-X 'server-mgr/cmd.defaultDockerMirrorsRaw=$(DOCKER_MIRRORS)'

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

deploy: build
	ssh -p $(PORT) $(SERVER) "mkdir -p $(REMOTE_PATH)"
	scp -P $(PORT) $(BINARY) $(SERVER):$(REMOTE_PATH)/$(BINARY)

test:
	go test ./...

clean:
	rm -f $(BINARY)
