# syntax=docker/dockerfile:1.7
FROM docker:29-cli

RUN apk add --no-cache bash jq

ARG DEMO_VERSION=v0.1.0-alpha.1
ARG DEMO_REPOSITORY=ghcr.io/krishnangopal1810/ai-flight-recorder
ENV AFR_DEMO_VERSION=${DEMO_VERSION}
ENV AFR_DEMO_REPOSITORY=${DEMO_REPOSITORY}

COPY deploy/demo/flight-demo /usr/local/bin/flight-demo

ENTRYPOINT ["/usr/local/bin/flight-demo"]
CMD ["--help"]
