# syntax=docker/dockerfile:1
#
# Kyurem dashboard image: build the Vite app, serve it with nginx and
# proxy /api to the gateway's management listener.
# Build from the repository root:
#   docker build -f deploy/docker/dashboard.Dockerfile -t kyurem/dashboard .

FROM node:22-alpine AS build
WORKDIR /src

# Dependencies first so the layer caches.
COPY dashboard/package.json dashboard/package-lock.json ./dashboard/
WORKDIR /src/dashboard
RUN npm ci

# The API types are generated from the OpenAPI contract, so it must be in
# the build context.
COPY api/ /src/api/
COPY dashboard/ /src/dashboard/
RUN npm run gen:api && npm run build

FROM nginx:1.27-alpine
COPY deploy/nginx/dashboard.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dashboard/dist /usr/share/nginx/html
EXPOSE 80
