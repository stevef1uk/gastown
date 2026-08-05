# PingApp – Trivial MVP spec (pipeline-friendly)

## Goal

A tiny Python application with a single HTTP endpoint using FastAPI. Success for the automated pipeline means:

```bash
cd pingapp && pip install -r requirements.txt && pytest
```
passes, and `cd pingapp && uvicorn main:app --port 8080` serves the endpoint.

Keep implementations small and literal. No extra files or abstractions.

## Layout (implement beads only)

```
pingapp/
├── requirements.txt
├── main.py
└── test_main.py
```

## Dependencies (`pingapp/requirements.txt`)

```text
fastapi==0.100.0
uvicorn==0.23.1
pytest==7.4.0
httpx==0.24.1
```

## HTTP API

| Method | Path | Success |
|--------|------|---------|
| GET | `/ping` | 200, JSON `{"message": "pong"}` |

## `pingapp/main.py`

1. Import `FastAPI` from `fastapi`.
2. Create an app instance: `app = FastAPI()`.
3. Define a route `@app.get("/ping")` that returns `{"message": "pong"}`.

## `pingapp/test_main.py`

1. Import `TestClient` from `fastapi.testclient`, and `app` from `main`.
2. Create a `client = TestClient(app)`.
3. Define `def test_ping():`
4. Call `response = client.get("/ping")`.
5. Assert that `response.status_code == 200` and `response.json() == {"message": "pong"}`.

## Definition of done

1. `cd pingapp && pytest test_main.py` — passes.
2. `cd pingapp && uvicorn main:app --port 8080 & curl http://localhost:8080/ping` — returns `{"message": "pong"}`.

**CRITICAL NOTE FOR AGENTS**: DO NOT run `uvicorn` or any other HTTP server in the foreground during implementation or testing. Because it is a blocking server, it will block forever and cause you to hang indefinitely. Always run it in the background with `&` and test using `curl`, or rely entirely on `pytest`.

## Delivery Phases
1. **python-setup** — Initialize requirements.txt / venv
2. **core** — main.py (FastAPI app + endpoint)
3. **test** — test_main.py (unit tests)
4. **integration-test** — Full smoke test (run server + curl)

> **Why this order:** `python-setup` first establishes dependencies. `core` implements the FastAPI app and endpoint. `test` adds unit tests. `integration-test` runs the server and verifies the endpoint with curl.
