all: build test

RACE ?= 0
export CGO_ENABLED ?= 0
ifeq ($(RACE), 1)
	GO_RACE := -race
	export CGO_ENABLED := 1
else
	GO_RACE :=
endif

SHELL := /bin/bash
ALL_GO_FILES := $(shell find ./ -name '*.go') $(shell find ./ -type f -wholename '*/embedded/*')

current_dir = $(shell pwd)

.PHONY: build
build: binaries build-static-demo

.PHONY: clean
clean:
	go clean
	rm -rf bin

.PHONY: test
test:
	go vet ./...
	go test -cover $(GO_RACE) -parallel 10 ./...

.PHONY: format
format:
	go fmt ./...

.PHONY: binaries
binaries: binaries_demo

.PHONY: binaries_demo
binaries_demo: bin/demo/app bin/demo/web/app.wasm

bin:
	mkdir -p $@

bin/demo: bin
	mkdir -p $@

bin/demo/app: bin/demo $(ALL_GO_FILES)
	go build -o $@ ./cmd/demo/...

bin/demo/web: bin/demo
	mkdir -p $@

bin/demo/web/app.wasm: bin/demo/web $(ALL_GO_FILES)
	GOARCH=wasm GOOS=js go build -o $@ ./cmd/demo/...

.PHONY: build-static-demo
build-static-demo: binaries_demo
	mkdir -p bin/static-demo
	go build -o bin/static-demo/app ./cmd/demo/...
	mkdir -p bin/static-demo/web
	GOARCH=wasm GOOS=js go build -o bin/static-demo/web/app.wasm ./cmd/demo/...
	cd bin/static-demo && GENERATE_STATIC_FILES=true ./app
	rm -f bin/static-demo/app

.PHONY: run-demo
run-demo: binaries_demo
	cd bin/demo && ./app

.PHONY: watch-run-demo
watch-run-demo:
	@PID=; \
	trap 'kill $$PID' TERM INT; \
	while true; do \
		$(MAKE) binaries_demo; \
		ok=$$?; \
		command=$$(if [ $$ok -eq 0 ]; then echo "./app"; else echo "sleep infinity"; fi); \
		pushd bin/demo; \
		$$command & PID=$$!; \
		popd; \
		inotifywait -qre close_write .; \
		kill $$PID; \
	done
