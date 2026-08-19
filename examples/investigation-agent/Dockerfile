# syntax=docker/dockerfile:1.7
FROM ghcr.io/astral-sh/uv:0.12.5 AS uv

FROM python:3.13-slim-bookworm
COPY --from=uv /uv /usr/local/bin/uv
WORKDIR /sdk
COPY sdk/python/pyproject.toml sdk/python/uv.lock ./
COPY sdk/python/src ./src
RUN uv sync --frozen --no-dev --extra reference

WORKDIR /app
COPY examples/investigation-agent/agent.py ./agent.py
ENV PATH="/sdk/.venv/bin:${PATH}"
USER 65532:65532
ENTRYPOINT ["python", "/app/agent.py"]
