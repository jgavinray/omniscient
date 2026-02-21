.PHONY: build test install clean setup-hooks

build:
	go build -o bin/omniscient ./cmd/omniscient

test:
	go test -v ./...

install:
	sudo mkdir -p /opt/omniscient/{data,credentials}
	sudo cp bin/omniscient /usr/local/bin/
	sudo cp config.yaml.example /opt/omniscient/config.yaml
	@echo "Edit /opt/omniscient/config.yaml before running"

clean:
	rm -rf bin/

setup-hooks:
	git config core.hooksPath .githooks
	@echo "Git hooks configured"
