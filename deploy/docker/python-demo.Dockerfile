FROM python:3.12-slim

WORKDIR /app
COPY demo/agents/python/pyproject.toml demo/agents/python/uv.lock ./
COPY demo/agents/python/src ./src
RUN pip install --no-cache-dir uv==0.9.28 \
    && uv sync --frozen --no-dev --no-editable
ENV PATH="/app/.venv/bin:${PATH}"
USER 65532:65532
EXPOSE 9012
ENTRYPOINT ["python", "-m", "kyurem_demo"]
CMD ["echo", "--host", "0.0.0.0", "--port", "9012"]
