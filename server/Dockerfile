# syntax=docker/dockerfile:1

# Build from server/:
#   docker build -t swift-stream-api .
# For Fargate on Graviton (ARM), or x86:
#   docker build --platform linux/arm64 -t swift-stream-api .
#   docker build --platform linux/amd64 -t swift-stream-api .
# Run (AWS credentials and region from the environment; boto3 reads the region
# from AWS_DEFAULT_REGION, not AWS_REGION):
#   docker run --rm -p 8080:8080 -e UPLOAD_BUCKET=<bucket> -e AWS_DEFAULT_REGION=<region> \
#     -e AWS_ACCESS_KEY_ID -e AWS_SECRET_ACCESS_KEY -e AWS_SESSION_TOKEN swift-stream-api

ARG PYTHON_VERSION=3.13

# Build stage: installs the dependencies into a virtualenv, which the runtime
# stage copies whole. pip, its cache, and requirements.txt stay behind.
FROM python:${PYTHON_VERSION}-slim AS build
WORKDIR /src
RUN python -m venv /opt/venv
ENV PATH=/opt/venv/bin:$PATH

# Dependencies first, so editing code does not reinstall them. They are read
# from pyproject.toml so the list lives in one place. There is no lock file,
# so each build gets the newest versions the ranges allow.
COPY pyproject.toml ./
RUN python -c "import tomllib; print('\n'.join(tomllib.load(open('pyproject.toml', 'rb'))['project']['dependencies']))" > requirements.txt
RUN --mount=type=cache,target=/root/.cache/pip pip install -r requirements.txt

# Runtime stage. Every dependency ships prebuilt wheels for amd64 and arm64,
# so neither stage needs a compiler.
FROM python:${PYTHON_VERSION}-slim

# PYTHONUNBUFFERED sends log lines to stdout (and so CloudWatch) as they are
# written instead of in blocks. No .pyc files are written at run time: the
# code directory is read-only to the app user.
ENV PATH=/opt/venv/bin:$PATH \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

RUN useradd --system --no-create-home --uid 10001 api

COPY --from=build /opt/venv /opt/venv
WORKDIR /srv
# The app package is run from here, not installed: `python -m app` finds it
# because the working directory is on sys.path.
COPY app ./app

USER api
EXPOSE 8080

# Exec form, so Python is PID 1 and receives the SIGTERM that ECS sends.
# Uvicorn then stops taking connections and gives in-flight requests 10
# seconds (app/__main__.py), well inside ECS's default 30-second stopTimeout.
ENTRYPOINT ["python", "-m", "app"]
