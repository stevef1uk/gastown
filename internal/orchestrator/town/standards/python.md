## Town Hall Coding Standards (Python)

- **Type hints & models:** Use Python 3.11+ type annotations (`list[T]`, `dict[K, V]`, `T | None`). Use Pydantic v2 models (`model_dump()`, `ConfigDict`) or `dataclasses` for structured payloads.
- **Imports & layout:** Use package-qualified imports relative to `{{layout_root}}` (never ad-hoc `sys.path` hacks). Avoid mutable default arguments (`def fn(items: list | None = None)`).
- **Web / FastAPI:** Return explicit HTTP status codes (`status_code=201`, `HTTPException`). Mount static files after API routes so `/static` or `/` never shadows REST endpoints.
- **Testing:** Write deterministic `pytest` functions or `unittest.TestCase` classes; use `fastapi.testclient.TestClient` or `httpx` for API tests and isolate state per test via fixtures.
