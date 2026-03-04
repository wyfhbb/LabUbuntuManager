.PHONY: build deploy clean

BINARY=server-mgr
SERVER=wyf@222.199.230.161
PORT=40903
REMOTE_PATH=~/lab_manager

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(BINARY) .

deploy: build
	ssh -p $(PORT) $(SERVER) "mkdir -p $(REMOTE_PATH)"
	scp -P $(PORT) $(BINARY) $(SERVER):$(REMOTE_PATH)/$(BINARY)

clean:
	rm -f $(BINARY)