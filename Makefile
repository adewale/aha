.PHONY: help verify verify-quick verify-full verify-ci verify-fuzz verify-race verify-build verify-mutation-dry verify-mutation

help:
	@printf '%s\n' \
	  'Targets:' \
	  '  verify              full local verification' \
	  '  verify-quick        go test ./... + whitespace checks' \
	  '  verify-full         quick + vet + race + fuzz + build' \
	  '  verify-ci           same as full, used by CI' \
	  '  verify-fuzz         bounded fuzz suite' \
	  '  verify-race         race detector' \
	  '  verify-build        build cmd/aha' \
	  '  verify-mutation-dry gremlins dry-run inventory' \
	  '  verify-mutation     gremlins mutation run on critical packages'

verify: verify-full

verify-quick:
	./scripts/verify.sh quick

verify-full:
	./scripts/verify.sh full

verify-ci:
	./scripts/verify.sh ci

verify-fuzz:
	./scripts/verify.sh fuzz

verify-race:
	./scripts/verify.sh race

verify-build:
	./scripts/verify.sh build

verify-mutation-dry:
	./scripts/verify.sh mutation-dry

verify-mutation:
	./scripts/verify.sh mutation
