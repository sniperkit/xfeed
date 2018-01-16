.PHONY: test deps

PKGS=`go list ./... | grep -v /vendor/`
LOCALS=`find . -type f -name '*.go' -not -path "./vendor/*"`

all: deps fmt test build

deps:
	@go list github.com/mjibson/esc || go get github.com/mjibson/esc/...
	@go list golang.org/x/tools/cmd/goimports || go get golang.org/x/tools/cmd/goimports
	go generate -x ./...
	go get ./...

clean-bundle:
	-rm -rf public

clean:
	-rm -rf bin

fmt:
	goimports -w $(LOCALS)
	go vet $(PKGS)

test:
	go test --tags json1 $(PKGS)

build: deps fmt
	test -d pivot && go build --tags json1 -o bin/`basename ${PWD}` pivot/*.go

quickbuild: deps fmt
	test -d pivot && go build -o bin/`basename ${PWD}` pivot/*.go
