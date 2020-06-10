export GO111MODULE=on
export CGO_ENABLED=0
export GOOS?=$(shell uname | awk '{print tolower($$0)}')
GOVER=$(shell cat go.mod| grep -e "^go " | cut -d" " -f2)
GOFILES=$(shell find . -type f -name '*.go' -not -path "./.git/*")

fmt:
	go fmt ./...

fmtcheck:
	([ -z "$(shell gofmt -d $(GOFILES))" ]) || (echo "Source is unformatted, please execute make format"; exit 1)

vet:
	go vet ./...

test:
	go test -timeout 30s -covermode=atomic -coverprofile=cover.out ./...

build: fmtcheck vet test
	go mod tidy
	go mod vendor
	go build -o build/snap-o-matic main.go

build-docker:
	@#USER_NS='-u $(shell id -u $(whoami)):$(shell id -g $(whoami))'
	docker run \
		--rm \
		${USER_NS} \
		-w /go/src/github.com/exoscale-labs/snap-o-matic \
		-e GOOS=${GOOS} \
		golang:${GOVER} \
		make build
