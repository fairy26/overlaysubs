.PHONY: build build-go build-helper test test-go test-helper run clean

PKG_CONFIG_PATH := /opt/homebrew/lib/pkgconfig

build: build-helper build-go

build-helper:
	cd helper && PKG_CONFIG_PATH=$(PKG_CONFIG_PATH) swift build -c release
	mkdir -p bin
	cp helper/.build/release/capture-helper bin/capture-helper

build-go:
	mkdir -p bin
	CGO_ENABLED=0 go build -o bin/overlaysubs ./cmd/overlaysubs

test: test-go test-helper

test-go:
	CGO_ENABLED=0 go test -v ./...

test-helper:
	cd helper && PKG_CONFIG_PATH=$(PKG_CONFIG_PATH) swift test

run: build
	./bin/overlaysubs

clean:
	rm -rf bin helper/.build
