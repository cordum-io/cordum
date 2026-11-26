# syntax=docker/dockerfile:1
# dist/ is static HTML, JS and CSS: one native build serves every target platform.
# Keep pnpm, tsc and Vite out of QEMU, where an arm64 Vite build stalled (task-efd2850d).
# Only /app/dist may cross from this builder into the target-platform runtime.
FROM --platform=$BUILDPLATFORM node:22-alpine AS builder
WORKDIR /app
ARG PNPM_VERSION=9.15.9

RUN corepack enable && corepack prepare pnpm@${PNPM_VERSION} --activate

COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY . .
RUN pnpm run build

FROM nginxinc/nginx-unprivileged:1.30-alpine3.24
USER root
# Refresh Alpine packages so fixed OS CVEs in the base image do not block release scans.
RUN apk upgrade --no-cache && apk add --no-cache curl gettext
COPY --from=builder --chown=101:101 /app/dist /usr/share/nginx/html
COPY --chown=101:101 nginx.conf /etc/nginx/conf.d/default.conf
COPY --chown=101:101 nginx.conf.template /etc/nginx/templates/nginx.conf.template
COPY --chown=101:101 entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
USER 101

EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
