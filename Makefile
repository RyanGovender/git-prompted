VERSION := $(shell cat VERSION)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: build test sync check dist clean

build: ## local binary in dist/ (the shims pick it up automatically)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/git-prompted ./cmd/git-prompted

test:
	go vet ./...
	go test ./...
	sh -n bin/git-prompted

sync:
	sh scripts/sync.sh

check: sync ## fail if generated copies are stale (used in CI)
	git diff --exit-code -- bin integrations GEMINI.md gemini-extension.json

dist: clean
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=""; [ $$os = windows ] && ext=.exe; \
	  echo "building $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/git-prompted-$$os-$$arch$$ext ./cmd/git-prompted || exit 1; \
	done
	cd dist && sha256sum git-prompted-* > checksums.txt

clean:
	rm -rf dist
