## Town Hall Coding Standards (Docker & Compose)

- **Real application runtime:** `Dockerfile` and `docker-compose.yml` must build and launch the real server on the configured port (never `sleep infinity` or stub echo containers).
- **Deterministic builds:** Copy dependency manifests (`go.mod`/`go.sum`, `requirements.txt`, `package.json`) and install dependencies before copying full source trees to maximize layer caching.
- **Networking & health:** Expose the exact port from `SPEC.md`, bind the server to `0.0.0.0`, and align `docker-compose.yml` service names, ports, and `working_dir` with the test harness.
