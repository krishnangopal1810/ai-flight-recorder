# syntax=docker/dockerfile:1.7
FROM golang:1.26-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/flight ./cmd/flight \
	&& CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/flightd ./cmd/flightd \
	&& CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/flight-gateway ./cmd/flight-gateway \
	&& CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/flight-fake ./cmd/flight-fake

FROM node:22-bookworm-slim AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

FROM docker:28-cli AS docker-cli

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-build /out/flight /flight
COPY --from=go-build /out/flightd /flightd
COPY --from=go-build /out/flight-gateway /flight-gateway
COPY --from=go-build /out/flight-fake /flight-fake
COPY --from=web-build /src/web/dist /web
COPY --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
USER 65532:65532
EXPOSE 7777 4317 4318 8080
ENTRYPOINT ["/flightd"]
