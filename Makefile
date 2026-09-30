# Local quality gates. Mirrors what CI enforces; run before pushing.
.PHONY: check fmt vet test

check: fmt vet test

fmt:
	@test -z "$$(gofmt -l internal/ cmd/)" || (echo "files need gofmt:"; gofmt -l internal/ cmd/; exit 1)

vet:
	go vet ./...

test:
	go test -count=1 ./...
