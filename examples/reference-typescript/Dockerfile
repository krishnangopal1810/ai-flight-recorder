# syntax=docker/dockerfile:1.7
FROM node:22-bookworm-slim AS build

WORKDIR /sdk/typescript
COPY sdk/typescript/package.json sdk/typescript/package-lock.json sdk/typescript/tsconfig.json ./
RUN npm ci
COPY sdk/typescript/src ./src
RUN npm run build

WORKDIR /app
COPY examples/reference-typescript/package.json examples/reference-typescript/package-lock.json examples/reference-typescript/tsconfig.json ./
RUN npm ci --install-links
COPY examples/reference-typescript/agent.ts ./agent.ts
RUN npm run build

FROM node:22-bookworm-slim AS production-dependencies
WORKDIR /sdk/typescript
COPY sdk/typescript/package.json ./package.json
COPY --from=build /sdk/typescript/dist ./dist
WORKDIR /app
COPY examples/reference-typescript/package.json examples/reference-typescript/package-lock.json ./
RUN npm ci --omit=dev --install-links

FROM node:22-bookworm-slim
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY --from=production-dependencies /app/node_modules ./node_modules
USER 65532:65532
ENTRYPOINT ["node", "/app/dist/agent.js"]
