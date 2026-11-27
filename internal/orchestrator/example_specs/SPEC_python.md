# PingApp – Trivial MVP spec (pipeline-friendly)

## Goal

A tiny Python application with a single HTTP endpoint using FastAPI. Success for the automated pipeline means:

```bash
cd pingapp && pip install -r requirements.txt && pytest
```
passes, and `cd pingapp && uvicorn main:app --host 127.0.0.1 --port 8080` serves the endpoint on `127.0.0.1:8080`.

Keep implementations small and literal. No extra files or abstractions.

## Layout (Create only these files)

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

1. `cd pingapp && pytest` — passes.
2. `cd pingapp && uvicorn main:app --host 127.0.0.1 --port 8080 & sleep 1 && curl -sf http://127.0.0.1:8080/ping` — returns `{"message": "pong"}`.

**CRITICAL NOTE FOR AGENTS**: DO NOT run `uvicorn` or any other HTTP server in the foreground during implementation or testing. Because it is a blocking server, it will block forever and cause you to hang indefinitely. Always run it in the background with `&` and test using `curl`, or rely entirely on `pytest`.

## Delivery Phases
1. **python-setup** — Initialize `pingapp/requirements.txt` and Python venv (verify: `cd pingapp && pip install -r requirements.txt`)
2. **core** — Implement `pingapp/main.py` (FastAPI app + `/ping` endpoint) (verify: `cd pingapp && python3 -m py_compile main.py`)
3. **test** — Implement `pingapp/test_main.py` (unit tests + runtime smoke) (verify: `cd pingapp && pytest`)

> **Why this order:** `python-setup` first establishes dependencies. `core` implements the FastAPI app and endpoint. `test` adds unit tests and runs the full test suite and smoke check.
