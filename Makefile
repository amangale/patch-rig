BINARY := bin/agent

.PHONY: build test vet run-task1 clean

build:
	go build -o $(BINARY) ./cmd/agent

test:
	go test ./...

vet:
	go vet ./...

run-task1: build
	./$(BINARY) -task eval-tasks/task01.jsonl -repo scratch-repo

clean:
	rm -rf bin