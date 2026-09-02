#!/bin/bash
cd /gptgrok2api-go/web-vue
docker run --rm -v /gptgrok2api-go/web-vue:/app -w /app node:22-alpine sh -c 'npm ci --no-audit --no-fund 2>&1 | tail -2 && npx tsc --noEmit 2>&1 | head -60; echo "TSC_EXIT=$?"' 2>&1 | tail -70
