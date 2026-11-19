include ../extension.mk

.PHONY: check
check:
	go test ./...
	@if [ ! -f ../../packages/typescript/dist/index.js ]; then \
		(cd ../../packages/typescript && pnpm install --frozen-lockfile && pnpm run build); \
	fi
	@if [ ! -f ../../packages/svelte/dist/build/module.js ]; then \
		(cd ../../packages/svelte && pnpm install --frozen-lockfile --ignore-scripts && pnpm run build); \
	fi
	cd ui && pnpm install --no-frozen-lockfile && pnpm run check
