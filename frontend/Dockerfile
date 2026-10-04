FROM node:26.10.0-alpine3.23@sha256:c3c6e314fd42e41962360b2482fc18d150beb47976c3aa7b8b9689d7ef42a5c2 AS builder

WORKDIR /app

ENV NEXT_TELEMETRY_DISABLED=1
ENV NPM_CONFIG_UPDATE_NOTIFIER=false

ARG SOURCE_REVISION
ARG SOURCE_DIRTY
ARG LOCKFILE_SHA256
ARG CONTEXT_SHA256
ARG SOURCE_DATE_EPOCH

COPY package.json package-lock.json ./

# No identity args means an unverified developer build with empty source identity labels.
RUN node -e 'const fs = require("node:fs"); \
    const crypto = require("node:crypto"); \
    const identity = ["SOURCE_REVISION", "SOURCE_DIRTY", "LOCKFILE_SHA256", "CONTEXT_SHA256"]; \
    if (identity.some(name => process.env[name] !== undefined)) { \
      for (const [name, length] of [["SOURCE_REVISION", 40], ["LOCKFILE_SHA256", 64], ["CONTEXT_SHA256", 64]]) { \
        const value = process.env[name] || ""; \
        if (value.length !== length || /[^0-9a-f]/.test(value)) throw new Error(name + " must be a lowercase hex digest of length " + length); \
      } \
      if (!["true", "false"].includes(process.env.SOURCE_DIRTY)) throw new Error("SOURCE_DIRTY must be true or false"); \
      const actual = crypto.createHash("sha256").update(fs.readFileSync("package-lock.json")).digest("hex"); \
      if (actual !== process.env.LOCKFILE_SHA256) throw new Error("LOCKFILE_SHA256 does not match package-lock.json"); \
    }'

RUN npm ci --ignore-scripts --no-audit --no-fund

ARG NEXT_PUBLIC_API_URL
ARG NEXT_PUBLIC_ADMIN_API_URL
ARG NEXT_PUBLIC_SOURCE_FILE_ORIGINS
ARG BACKEND_PORT=8080
ARG BACKEND_URL=http://backend:8080

ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL
ENV NEXT_PUBLIC_ADMIN_API_URL=$NEXT_PUBLIC_ADMIN_API_URL
ENV NEXT_PUBLIC_SOURCE_FILE_ORIGINS=$NEXT_PUBLIC_SOURCE_FILE_ORIGINS
ENV BACKEND_PORT=$BACKEND_PORT
ENV BACKEND_URL=$BACKEND_URL

COPY . .
RUN npm run build

FROM node:26.10.0-alpine3.23@sha256:c3c6e314fd42e41962360b2482fc18d150beb47976c3aa7b8b9689d7ef42a5c2 AS runner
WORKDIR /app

ARG SOURCE_REVISION
ARG SOURCE_DIRTY
ARG LOCKFILE_SHA256
ARG CONTEXT_SHA256
ARG FRONTEND_PORT=3000
ARG BACKEND_PORT=8080
ARG BACKEND_URL=http://backend:8080

LABEL org.opencontainers.image.revision="${SOURCE_REVISION}" \
    io.task-per-minute.source.dirty="${SOURCE_DIRTY}" \
    io.task-per-minute.lockfile.sha256="${LOCKFILE_SHA256}" \
    io.task-per-minute.context.sha256="${CONTEXT_SHA256}" \
    org.opencontainers.image.base.digest="sha256:9ec4a2e289874ed0d722e1772ec2de45d2801541db8612f3638b26f128c69ac2" \
    org.opencontainers.image.base.name="docker.io/library/node:24.21.0-alpine3.23"

RUN rm -rf -- /usr/local/lib/node_modules/npm \
    /usr/local/lib/node_modules/corepack \
    /opt/yarn-v1.22.22 && \
    rm -f -- /usr/local/bin/npm \
    /usr/local/bin/npx \
    /usr/local/bin/corepack \
    /usr/local/bin/pnpm \
    /usr/local/bin/pnpx \
    /usr/local/bin/yarn \
    /usr/local/bin/yarnpkg

RUN addgroup --system --gid 1001 nodejs && \
    adduser --system --uid 1001 --ingroup nodejs nextjs

COPY --from=builder /app/public ./public
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

USER nextjs

ENV PORT=${FRONTEND_PORT}
ENV HOSTNAME=0.0.0.0
ENV BACKEND_PORT=$BACKEND_PORT
ENV BACKEND_URL=$BACKEND_URL

EXPOSE ${FRONTEND_PORT}

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["node", "-e", "const http = require('node:http'); const request = http.get({ hostname: '127.0.0.1', port: process.env.PORT || 3000, path: '/health', timeout: 4000 }, response => { response.resume(); process.exit(response.statusCode === 200 ? 0 : 1); }); request.on('timeout', () => { request.destroy(); process.exit(1); }); request.on('error', () => process.exit(1));"]

CMD ["node", "server.js"]
