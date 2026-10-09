"""Run with `python -m app`. Listens on PORT, falling back to 8080."""

import os

import uvicorn

SHUTDOWN_TIMEOUT_SECONDS = 10

if __name__ == "__main__":
    # On SIGINT/SIGTERM uvicorn stops accepting connections and gives
    # in-flight requests up to the timeout to finish.
    uvicorn.run(
        "app.main:app",
        host="0.0.0.0",
        port=int(os.environ.get("PORT", "8080")),
        timeout_graceful_shutdown=SHUTDOWN_TIMEOUT_SECONDS,
    )
