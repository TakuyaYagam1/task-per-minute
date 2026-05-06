FROM node:24.15.0-alpine3.23 AS base

WORKDIR /app

COPY package*.json ./

RUN npm ci --omit=dev

FROM base AS builder
WORKDIR /app

COPY package*.json ./
RUN npm ci

ARG NEXT_PUBLIC_API_URL
ARG NEXT_PUBLIC_ADMIN_API_URL
ARG NEXT_PUBLIC_WS_URL
ARG BACKEND_PORT=8080
ARG BACKEND_URL=http://backend:8080

ENV NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL
ENV NEXT_PUBLIC_ADMIN_API_URL=$NEXT_PUBLIC_ADMIN_API_URL
ENV NEXT_PUBLIC_WS_URL=$NEXT_PUBLIC_WS_URL
ENV BACKEND_PORT=$BACKEND_PORT
ENV BACKEND_URL=$BACKEND_URL

COPY . .
RUN npm run build

FROM node:24.15.0-alpine3.23 AS runner
WORKDIR /app

ARG FRONTEND_PORT=3000
ARG BACKEND_PORT=8080
ARG BACKEND_URL=http://backend:8080

RUN apk add --no-cache wget && \
    addgroup --system --gid 1001 nodejs && \
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
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT:-3000}/health" || exit 1

CMD ["node", "server.js"]
