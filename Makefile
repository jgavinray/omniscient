VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: build test lint install clean setup-hooks

build:
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/omniscient ./cmd/omniscient

test:
	go test -race -v ./...

lint:
	go vet ./...
	go mod tidy -diff

install:
	sudo mkdir -p -m 700 /opt/omniscient/{data,credentials}
	sudo cp bin/omniscient /usr/local/bin/
	sudo install -m 600 config.yaml.example /opt/omniscient/config.yaml
	@echo "Edit /opt/omniscient/config.yaml before running"

clean:
	rm -rf bin/

setup-hooks:
	git config core.hooksPath .githooks
	@echo "Git hooks configured"
