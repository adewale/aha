.PHONY: help verify verify-quick verify-full verify-ci verify-fuzz verify-ts verify-race verify-build verify-mutation-dry verify-mutation gen-ts gen-docs

help:
	@printf '%s\n' \
	  'Targets:' \
	  '  verify              full local verification' \
	  '  verify-quick        go test ./... + whitespace checks' \
	  '  verify-full         quick + vet + race + fuzz + build' \
	  '  verify-ci           same as full, used by CI' \
	  '  verify-fuzz         bounded fuzz suite' \
	  '  verify-ts           typecheck + runtime-test the TypeScript client' \
	  '  verify-race         race detector' \
	  '  verify-build        build cmd/aha' \
	  '  verify-mutation-dry gremlins dry-run inventory' \
	  '  verify-mutation     gremlins mutation run on critical packages' \
	  '  gen-ts              regenerate clients/typescript/aha-mcp.ts' \
	  '  gen-docs            regenerate docs/commands.md'

verify: verify-full

verify-quick:
	./scripts/verify.sh quick

verify-full:
	./scripts/verify.sh full

verify-ci:
	./scripts/verify.sh ci

verify-fuzz:
	./scripts/verify.sh fuzz

verify-ts:
	./scripts/verify.sh ts

gen-ts:
	go run ./cmd/aha-gen-ts -out clients/typescript/aha-mcp.ts

gen-docs:
	go run ./cmd/aha-gen-docs -out docs/commands.md

verify-race:
	./scripts/verify.sh race

verify-build:
	./scripts/verify.sh build

verify-mutation-dry:
	./scripts/verify.sh mutation-dry

verify-mutation:
	./scripts/verify.sh mutation
