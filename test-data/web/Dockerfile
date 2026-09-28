# syntax=docker/dockerfile:1.7

ARG NODE_IMAGE=node:22-bookworm-slim

FROM ${NODE_IMAGE} AS dependencies

ARG TASK_DIR

WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 make g++ \
    && rm -rf /var/lib/apt/lists/*

COPY ${TASK_DIR}/package.json ${TASK_DIR}/package-lock.json ./

RUN npm ci --omit=dev \
    && npm cache clean --force

FROM ${NODE_IMAGE} AS runtime

ARG TASK_DIR
ARG TASK_PORT

ENV NODE_ENV=production

WORKDIR /app

COPY --from=dependencies --chown=node:node /app/node_modules ./node_modules
COPY --chown=node:node ${TASK_DIR}/ ./

USER node

EXPOSE ${TASK_PORT}

CMD ["node", "app.js"]
