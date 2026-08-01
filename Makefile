.PHONY: test-backend typecheck build-frontend check-installer verify

test-backend:
	go test ./internal/config ./internal/web/service ./internal/web/controller -count=1

typecheck:
	cd frontend && npm run typecheck

build-frontend:
	cd frontend && npm run build

check-installer:
	bash -n scripts/install-server.sh

verify: test-backend typecheck build-frontend check-installer
