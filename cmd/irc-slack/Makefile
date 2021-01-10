CMD=irc-slack

REVISION := $(shell git rev-parse --short HEAD)
BRANCH := $(shell git rev-parse --abbrev-ref HEAD)

all: build

build: $(wildcard *.go)
	CGO_ENABLED=0 go build -ldflags "-X main.Version=git-$(REVISION)_$(BRANCH)" -o $(CMD)
