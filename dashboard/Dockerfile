# syntax=docker/dockerfile:1
FROM node:22-alpine AS builder
WORKDIR /app
ARG PNPM_VERSION=9.15.9

RUN corepack enable && corepack prepare pnpm@${PNPM_VERSION} --activate

COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY . .
RUN pnpm run build

FROM nginxinc/nginx-unprivileged:1.27-alpine
USER root
RUN apk add --no-cache curl gettext
COPY --from=builder --chown=101:101 /app/dist /usr/share/nginx/html
COPY --chown=101:101 nginx.conf /etc/nginx/conf.d/default.conf
COPY --chown=101:101 nginx.conf.template /etc/nginx/templates/nginx.conf.template
COPY --chown=101:101 entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
USER 101

EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
