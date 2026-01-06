# syntax=docker/dockerfile:1
FROM node:20-alpine AS builder
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.25-alpine
USER root
COPY --from=builder --chown=101:101 /app/dist /usr/share/nginx/html
COPY --chown=101:101 nginx.conf /etc/nginx/conf.d/default.conf
COPY --chown=101:101 entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
USER 101

EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
